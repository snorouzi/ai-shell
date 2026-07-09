package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"ai-shell/internal/config"
	shellcontext "ai-shell/internal/context"
	"ai-shell/internal/daemon"
	"ai-shell/internal/execution"
)

const sessionEnv = "AI_SHELL_SESSION_ID"

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "ai: %v\n", err)
		os.Exit(1)
	}
}

type command struct {
	kind          string
	modelOverride string
	modelArg      string
	prompt        string
}

func run(args []string, stdout, stderr io.Writer) error {
	cmd, err := parseArgs(args, stderr)
	if err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	if cmd.kind == "daemon" {
		return daemon.Run(context.Background(), cfg)
	}

	if err := ensureDaemon(cfg, stderr); err != nil {
		return err
	}

	conn, err := net.DialTimeout("unix", cfg.Daemon.SocketPath, 2*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()

	return sendCommand(conn, cfg, sessionID(), cmd, stdout, stderr)
}

func parseArgs(args []string, stderr io.Writer) (command, error) {
	flags := flag.NewFlagSet("ai", flag.ContinueOnError)
	flags.SetOutput(stderr)

	var cmd command
	flags.StringVar(&cmd.modelOverride, "model", "", "use a model tag for this query only")

	if err := flags.Parse(args); err != nil {
		return command{}, err
	}

	remaining := flags.Args()
	if len(remaining) == 0 {
		return command{}, errors.New("usage: ai [--model <tag>] <query>")
	}

	switch remaining[0] {
	case "daemon":
		if len(remaining) != 1 {
			return command{}, errors.New("usage: ai daemon")
		}
		cmd.kind = "daemon"
	case "models":
		if len(remaining) != 1 {
			return command{}, errors.New("usage: ai models")
		}
		cmd.kind = "models"
	case "model":
		cmd.kind = "model"
		switch len(remaining) {
		case 1:
		case 2:
			cmd.modelArg = remaining[1]
		default:
			return command{}, errors.New("usage: ai model [profile-or-tag|reset]")
		}
	default:
		cmd.kind = "query"
		cmd.prompt = strings.Join(remaining, " ")
	}

	return cmd, nil
}

func ensureDaemon(cfg config.Config, stderr io.Writer) error {
	if conn, err := net.DialTimeout("unix", cfg.Daemon.SocketPath, 200*time.Millisecond); err == nil {
		conn.Close()
		return nil
	}

	executable, err := os.Executable()
	if err != nil {
		return err
	}

	cmd := exec.Command(executable, "daemon")
	cmd.Stdout = stderr
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return err
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", cfg.Daemon.SocketPath, 200*time.Millisecond)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	return fmt.Errorf("daemon did not become ready at %s", cfg.Daemon.SocketPath)
}

func sendCommand(conn net.Conn, cfg config.Config, sessionID string, cmd command, stdout, stderr io.Writer) error {
	req := requestForCommand(sessionID, cmd)
	if cmd.kind == "query" {
		req.Context = shellcontext.GatherClient(cfg.Context)
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return err
	}

	var spinner *thinkingSpinner
	if cmd.kind == "query" {
		spinner = newThinkingSpinner(stderr)
		spinner.Start()
		defer spinner.Stop()
	}

	decoder := json.NewDecoder(bufio.NewReader(conn))
	for {
		var resp daemon.Response
		if err := decoder.Decode(&resp); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}

		if spinner != nil && resp.Type != daemon.ResponseChunk {
			spinner.Stop()
		}

		done, err := handleResponse(conn, cfg, sessionID, stdout, resp, spinner)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}
}

func requestForCommand(sessionID string, cmd command) daemon.Request {
	switch cmd.kind {
	case "models":
		return daemon.Request{Type: daemon.RequestModelList, SessionID: sessionID}
	case "model":
		req := daemon.Request{Type: daemon.RequestModelSwitch, SessionID: sessionID}
		if cmd.modelArg == "" {
			req.Type = daemon.RequestModelCurrent
			return req
		}
		if cmd.modelArg == "reset" {
			req.Reset = true
			return req
		}
		req.Model = cmd.modelArg
		return req
	default:
		return daemon.Request{
			Type:      daemon.RequestQuery,
			SessionID: sessionID,
			Prompt:    cmd.prompt,
			Model:     cmd.modelOverride,
		}
	}
}

func handleResponse(conn net.Conn, cfg config.Config, sessionID string, stdout io.Writer, resp daemon.Response, spinner *thinkingSpinner) (bool, error) {
	switch resp.Type {
	case daemon.ResponseChunk:
		if spinner != nil {
			spinner.Stop()
		}
		_, err := fmt.Fprint(stdout, resp.Content)
		return false, err
	case daemon.ResponseDone:
		_, err := fmt.Fprintln(stdout)
		return true, err
	case daemon.ResponseError:
		return true, errors.New(resp.Error)
	case daemon.ResponseSuggestion:
		return true, handleSuggestion(conn, cfg, sessionID, stdout, resp)
	case daemon.ResponseModelSwitch:
		_, err := fmt.Fprintf(stdout, "current model: %s\n", resp.CurrentModel)
		return true, err
	case daemon.ResponseModel:
		_, err := fmt.Fprintf(stdout, "current model: %s\n", resp.CurrentModel)
		return true, err
	case daemon.ResponseModelList:
		return true, printModels(stdout, resp)
	default:
		return true, fmt.Errorf("unknown response type %q", resp.Type)
	}
}

// handleSuggestion shows a model-suggested command and, when execution is
// enabled, asks the user to confirm before sending execute_confirm back to the
// daemon. With execution disabled it only displays the suggestion.
func handleSuggestion(conn net.Conn, cfg config.Config, sessionID string, stdout io.Writer, resp daemon.Response) error {
	if resp.Explanation != "" {
		fmt.Fprintln(stdout, resp.Explanation)
	}
	for i, cmd := range resp.Commands {
		fmt.Fprintf(stdout, "  %d. %s\n", i+1, cmd)
	}
	fmt.Fprintf(stdout, "Risk: %s\n", resp.Risk)

	if !cfg.Execution.Enabled {
		return nil
	}

	approved, err := execution.Confirm(os.Stdin, stdout, execution.Suggestion{
		Explanation: resp.Explanation,
		Commands:    resp.Commands,
		Risk:        resp.Risk,
	}, cfg.Execution)
	if err != nil {
		return err
	}

	return json.NewEncoder(conn).Encode(daemon.Request{
		Type:      daemon.RequestExecuteConfirm,
		SessionID: sessionID,
		Approve:   approved,
	})
}

func printModels(stdout io.Writer, resp daemon.Response) error {
	if resp.CurrentModel != "" {
		if _, err := fmt.Fprintf(stdout, "current model: %s\n", resp.CurrentModel); err != nil {
			return err
		}
	}
	for _, profile := range resp.Profiles {
		status := "not installed"
		if profile.Installed {
			status = "installed"
		}
		if _, err := fmt.Fprintf(stdout, "%s\t%s\t%s\n", profile.Profile, profile.Model, status); err != nil {
			return err
		}
	}
	return nil
}

func sessionID() string {
	if value := os.Getenv(sessionEnv); value != "" {
		return value
	}
	return "default"
}

type thinkingSpinner struct {
	w      io.Writer
	done   chan struct{}
	once   sync.Once
	mu     sync.Mutex
	active bool
}

func newThinkingSpinner(w io.Writer) *thinkingSpinner {
	return &thinkingSpinner{
		w:    w,
		done: make(chan struct{}),
	}
}

func (s *thinkingSpinner) Start() {
	if s.w == nil {
		return
	}

	s.mu.Lock()
	s.active = true
	s.mu.Unlock()

	go func() {
		frames := []string{"|", "/", "-", "\\"}
		ticker := time.NewTicker(120 * time.Millisecond)
		defer ticker.Stop()

		i := 0
		fmt.Fprintf(s.w, "\r\033[2mThinking... %s\033[0m", frames[i])
		for {
			select {
			case <-ticker.C:
				i = (i + 1) % len(frames)
				fmt.Fprintf(s.w, "\r\033[2mThinking... %s\033[0m", frames[i])
			case <-s.done:
				return
			}
		}
	}()
}

func (s *thinkingSpinner) Stop() {
	if s == nil {
		return
	}

	s.once.Do(func() {
		close(s.done)
		s.mu.Lock()
		active := s.active
		s.active = false
		s.mu.Unlock()
		if active && s.w != nil {
			fmt.Fprint(s.w, "\r\033[0m\033[2K")
		}
	})
}
