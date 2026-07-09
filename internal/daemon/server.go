package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"ai-shell/internal/config"
	shellcontext "ai-shell/internal/context"
	"ai-shell/internal/ollama"
	"ai-shell/internal/session"
)

type OllamaClient interface {
	Generate(context.Context, ollama.GenerateRequest, func(ollama.GenerateChunk) error) error
	Tags(context.Context) ([]ollama.Model, error)
}

type Server struct {
	cfg      config.Config
	ollama   OllamaClient
	sessions *session.Store
	logger   *log.Logger
}

func NewServer(cfg config.Config, ollamaClient OllamaClient, logger *log.Logger) *Server {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	return &Server{
		cfg:      cfg,
		ollama:   ollamaClient,
		sessions: session.NewStore(cfg.Session.MaxHistoryMessages),
		logger:   logger,
	}
}

func Run(ctx context.Context, cfg config.Config) error {
	logger, closeLog, err := daemonLogger(cfg.Daemon.LogFile)
	if err != nil {
		return err
	}
	defer closeLog()

	ollamaClient, err := ollama.New(cfg.Ollama)
	if err != nil {
		return err
	}

	return NewServer(cfg, ollamaClient, logger).Serve(ctx, cfg.Daemon.SocketPath)
}

func (s *Server) Serve(ctx context.Context, socketPath string) error {
	if socketPath == "" {
		return errors.New("daemon.socket_path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		return err
	}
	if err := os.Remove(socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(socketPath)

	go func() {
		<-ctx.Done()
		listener.Close()
	}()

	s.logger.Printf("listening on %s", socketPath)
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go s.handleConn(ctx, conn)
	}
}

func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	var req Request
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&req); err != nil {
		writeResponse(conn, Response{Type: ResponseError, Error: err.Error()})
		return
	}
	if req.SessionID == "" {
		req.SessionID = "default"
	}

	switch req.Type {
	case RequestQuery:
		s.handleQuery(ctx, conn, req)
	case RequestModelSwitch:
		s.handleModelSwitch(ctx, conn, req)
	case RequestModelCurrent:
		s.handleModelCurrent(conn, req)
	case RequestModelList:
		s.handleModelList(ctx, conn, req)
	default:
		writeResponse(conn, Response{Type: ResponseError, Error: fmt.Sprintf("unknown request type %q", req.Type)})
	}
}

func (s *Server) handleQuery(ctx context.Context, w io.Writer, req Request) {
	model, err := s.modelForQuery(req.SessionID, req.Model)
	if err != nil {
		writeResponse(w, Response{Type: ResponseError, Error: err.Error()})
		return
	}

	requestContext := shellcontext.Enrich(ctx, s.cfg.Context, req.Context)
	systemPrompt := shellcontext.SystemPrompt(s.cfg.Context, requestContext)
	prompt := s.promptWithHistory(req.SessionID, req.Prompt)
	var assistant strings.Builder
	err = s.ollama.Generate(ctx, ollama.GenerateRequest{
		Model:  model,
		Prompt: prompt,
		System: systemPrompt,
	}, func(chunk ollama.GenerateChunk) error {
		if chunk.Response == "" {
			return nil
		}
		assistant.WriteString(chunk.Response)
		return writeResponse(w, Response{Type: ResponseChunk, Content: chunk.Response})
	})
	if err != nil {
		writeResponse(w, Response{Type: ResponseError, Error: err.Error()})
		return
	}

	s.sessions.AddMessages(req.SessionID,
		session.Message{Role: "user", Content: req.Prompt},
		session.Message{Role: "assistant", Content: assistant.String()},
	)
	writeResponse(w, Response{Type: ResponseDone})
}

func (s *Server) handleModelSwitch(ctx context.Context, w io.Writer, req Request) {
	if req.Reset {
		defaultModel, err := s.defaultModel()
		if err != nil {
			writeResponse(w, Response{Type: ResponseError, Error: err.Error()})
			return
		}
		s.sessions.SetCurrentModel(req.SessionID, "")
		writeResponse(w, Response{
			Type:         ResponseModelSwitch,
			CurrentModel: defaultModel,
			DefaultModel: defaultModel,
		})
		return
	}

	model, err := s.resolveModel(ctx, req.Model)
	if err != nil {
		writeResponse(w, Response{Type: ResponseError, Error: err.Error()})
		return
	}

	s.sessions.SetCurrentModel(req.SessionID, model)
	defaultModel, err := s.defaultModel()
	if err != nil {
		writeResponse(w, Response{Type: ResponseError, Error: err.Error()})
		return
	}
	writeResponse(w, Response{
		Type:         ResponseModelSwitch,
		CurrentModel: model,
		DefaultModel: defaultModel,
	})
}

func (s *Server) handleModelCurrent(w io.Writer, req Request) {
	currentModel, err := s.effectiveSessionModel(req.SessionID)
	if err != nil {
		writeResponse(w, Response{Type: ResponseError, Error: err.Error()})
		return
	}
	defaultModel, err := s.defaultModel()
	if err != nil {
		writeResponse(w, Response{Type: ResponseError, Error: err.Error()})
		return
	}
	writeResponse(w, Response{
		Type:         ResponseModel,
		CurrentModel: currentModel,
		DefaultModel: defaultModel,
	})
}

func (s *Server) handleModelList(ctx context.Context, w io.Writer, req Request) {
	installed, err := s.installedModels(ctx)
	if err != nil {
		writeResponse(w, Response{Type: ResponseError, Error: err.Error()})
		return
	}

	profiles := make([]ModelProfile, 0, len(s.cfg.Ollama.Models))
	for _, profile := range sortedKeys(s.cfg.Ollama.Models) {
		model := s.cfg.Ollama.Models[profile]
		profiles = append(profiles, ModelProfile{
			Profile:   profile,
			Model:     model,
			Installed: installed[model],
		})
	}

	currentModel, err := s.effectiveSessionModel(req.SessionID)
	if err != nil {
		writeResponse(w, Response{Type: ResponseError, Error: err.Error()})
		return
	}
	defaultModel, err := s.defaultModel()
	if err != nil {
		writeResponse(w, Response{Type: ResponseError, Error: err.Error()})
		return
	}

	writeResponse(w, Response{
		Type:         ResponseModelList,
		CurrentModel: currentModel,
		DefaultModel: defaultModel,
		Profiles:     profiles,
	})
}

func (s *Server) modelForQuery(sessionID, override string) (string, error) {
	if override != "" {
		return override, nil
	}
	return s.effectiveSessionModel(sessionID)
}

func (s *Server) effectiveSessionModel(sessionID string) (string, error) {
	if model := s.sessions.CurrentModel(sessionID); model != "" {
		return model, nil
	}
	return s.defaultModel()
}

func (s *Server) defaultModel() (string, error) {
	if s.cfg.Ollama.DefaultProfile == "" {
		return "", errors.New("ollama.default_profile is empty")
	}
	model, ok := s.cfg.Ollama.Models[s.cfg.Ollama.DefaultProfile]
	if !ok || model == "" {
		return "", fmt.Errorf("ollama.default_profile %q is not defined in ollama.models", s.cfg.Ollama.DefaultProfile)
	}
	return model, nil
}

func (s *Server) resolveModel(ctx context.Context, profileOrTag string) (string, error) {
	if profileOrTag == "" {
		return "", errors.New("model is required")
	}
	if model, ok := s.cfg.Ollama.Models[profileOrTag]; ok {
		profileOrTag = model
	}

	installed, err := s.installedModels(ctx)
	if err != nil {
		return "", err
	}
	if !installed[profileOrTag] {
		return "", fmt.Errorf("model %q is not installed in Ollama", profileOrTag)
	}
	return profileOrTag, nil
}

func (s *Server) installedModels(ctx context.Context) (map[string]bool, error) {
	models, err := s.ollama.Tags(ctx)
	if err != nil {
		return nil, err
	}
	installed := make(map[string]bool, len(models))
	for _, model := range models {
		if model.Name != "" {
			installed[model.Name] = true
		}
		if model.Model != "" {
			installed[model.Model] = true
		}
	}
	return installed, nil
}

func (s *Server) promptWithHistory(sessionID, prompt string) string {
	messages := s.sessions.Messages(sessionID)
	if len(messages) == 0 {
		return prompt
	}

	var b strings.Builder
	b.WriteString("Conversation so far:\n")
	for _, message := range messages {
		b.WriteString(message.Role)
		b.WriteString(": ")
		b.WriteString(message.Content)
		b.WriteString("\n")
	}
	b.WriteString("\nuser: ")
	b.WriteString(prompt)
	b.WriteString("\nassistant:")
	return b.String()
}

func writeResponse(w io.Writer, resp Response) error {
	return json.NewEncoder(w).Encode(resp)
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func daemonLogger(path string) (*log.Logger, func(), error) {
	if path == "" {
		return log.New(io.Discard, "", 0), func() {}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, nil, err
	}
	return log.New(file, "", log.LstdFlags), func() { file.Close() }, nil
}
