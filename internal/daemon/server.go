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
	"sync"

	"ai-shell/internal/config"
	shellcontext "ai-shell/internal/context"
	"ai-shell/internal/execution"
	"ai-shell/internal/ollama"
	"ai-shell/internal/preflight"
	"ai-shell/internal/session"
	"ai-shell/internal/web"
)

type OllamaClient interface {
	Generate(context.Context, ollama.GenerateRequest, func(ollama.GenerateChunk) error) error
	Tags(context.Context) ([]ollama.Model, error)
}

// webToolInstruction tells the model it may call the web_search tool by
// replying with a single JSON object. This is the "basic function-calling
// loop" the spec describes, implemented against the generate endpoint.
const webToolInstruction = "You have access to a web search tool. If the user's question " +
	"needs current or live information (news, recent releases, documentation, prices, etc.), " +
	`respond with ONLY a JSON object: {"web_search": "the search query"}. ` +
	"Otherwise answer the user directly in plain text."

type Server struct {
	cfg      config.Config
	ollama   OllamaClient
	sessions *session.Store
	web      *web.Client
	logger   *log.Logger

	// mu guards mutable access to cfg.Ollama.Models, which the model-add
	// command updates at runtime while other requests read it concurrently.
	mu sync.RWMutex
}

func NewServer(cfg config.Config, ollamaClient OllamaClient, logger *log.Logger) *Server {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	server := &Server{
		cfg:      cfg,
		ollama:   ollamaClient,
		sessions: session.NewStore(cfg.Session.MaxHistoryMessages),
		logger:   logger,
	}
	if cfg.Web.Enabled {
		if wc, err := web.New(cfg.Web); err != nil {
			logger.Printf("web search disabled: %v", err)
		} else {
			server.web = wc
		}
	}
	return server
}

func Run(ctx context.Context, cfg config.Config) error {
	cfg, err := preflight.Run(ctx, cfg, os.Stdin, os.Stdout)
	if err != nil {
		return err
	}

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
	case RequestModelAdd:
		s.handleModelAdd(ctx, conn, req)
	case RequestModelRemove:
		s.handleModelRemove(conn, req)
	case RequestExecuteConfirm:
		s.handleExecuteConfirm(ctx, conn, req)
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

	// Optional live context. When web search is enabled this runs the model's
	// "do I need current information?" decision and, if so, queries SearXNG.
	// The returned context composes with both chat and execution below, so the
	// two features are no longer mutually exclusive.
	webContext, err := s.maybeWebSearch(ctx, w, req, model)
	if err != nil {
		return
	}

	if s.cfg.Execution.Enabled {
		s.handleExecQuery(ctx, w, req, model, webContext)
		return
	}

	s.handleChatQuery(ctx, w, req, model, webContext)
}

// maybeWebSearch runs the web-search decision loop. It returns formatted search
// results to fold into the final answer, or "" when the model does not need
// live information. A non-nil error means a fatal condition was already written
// to the connection (search failure), and the caller should stop.
func (s *Server) maybeWebSearch(ctx context.Context, w io.Writer, req Request, model string) (string, error) {
	if !s.cfg.Web.Enabled || s.web == nil {
		return "", nil
	}

	requestContext := shellcontext.Enrich(ctx, s.cfg.Context, req.Context)
	system := shellcontext.SystemPrompt(s.cfg.Context, requestContext)
	if system != "" {
		system += "\n\n"
	}
	system += webToolInstruction

	// Turn 1: let the model decide whether to call the web_search tool.
	var first strings.Builder
	err := s.ollama.Generate(ctx, ollama.GenerateRequest{
		Model:  model,
		Prompt: s.promptWithHistory(req.SessionID, req.Prompt),
		System: system,
	}, func(chunk ollama.GenerateChunk) error {
		first.WriteString(chunk.Response)
		return nil
	})
	if err != nil {
		writeResponse(w, Response{Type: ResponseError, Error: err.Error()})
		return "", err
	}

	query, wantsSearch := web.ParseToolCall(first.String())
	if !wantsSearch {
		return "", nil
	}

	// Signal the client so it can show a "searching" indicator during the
	// (potentially slow) network round-trip to SearXNG.
	writeResponse(w, Response{Type: ResponseSearching})

	results, serr := s.web.Search(ctx, query)
	if serr != nil {
		// Web search is optional: don't abort the whole query on failure.
		// Tell the client what happened and fall back to answering without
		// live context.
		writeResponse(w, Response{Type: ResponseNotice, Content: "web search unavailable: " + serr.Error()})
		return "", nil
	}

	return web.Format(results), nil
}

// handleChatQuery streams a plain-text answer, optionally informed by web
// search results when web search was used.
func (s *Server) handleChatQuery(ctx context.Context, w io.Writer, req Request, model, webContext string) {
	requestContext := shellcontext.Enrich(ctx, s.cfg.Context, req.Context)
	system := shellcontext.SystemPrompt(s.cfg.Context, requestContext)
	prompt := s.promptWithHistory(req.SessionID, req.Prompt)
	if webContext != "" {
		prompt += "\n\n" + webContext +
			"\n\nAnswer the user's question using the search results above."
	}

	var assistant strings.Builder
	err := s.ollama.Generate(ctx, ollama.GenerateRequest{
		Model:  model,
		Prompt: prompt,
		System: system,
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

// handleExecQuery asks the model for a structured command suggestion (JSON),
// classifies its risk from the config rules, and returns it as a suggestion the
// client can confirm. Plain-text or command-less answers fall back to a normal
// response so execution never breaks ordinary chat. When webContext is non-empty
// the model is also told to use the live search results it contains.
func (s *Server) handleExecQuery(ctx context.Context, w io.Writer, req Request, model, webContext string) {
	requestContext := shellcontext.Enrich(ctx, s.cfg.Context, req.Context)
	system := shellcontext.SystemPrompt(s.cfg.Context, requestContext)
	if system != "" {
		system += "\n\n"
	}
	system += execution.SystemPrompt()
	if webContext != "" {
		system += "\n\n" + webContext +
			"\n\nUse the search results above when they are relevant to the command."
	}

	var raw strings.Builder
	err := s.ollama.Generate(ctx, ollama.GenerateRequest{
		Model:  model,
		Prompt: s.promptWithHistory(req.SessionID, req.Prompt),
		System: system,
	}, func(chunk ollama.GenerateChunk) error {
		raw.WriteString(chunk.Response)
		return nil
	})
	if err != nil {
		writeResponse(w, Response{Type: ResponseError, Error: err.Error()})
		return
	}

	sug, perr := parseSuggestion(raw.String())
	if perr != nil {
		// Could not parse JSON: echo the model's raw text as a normal answer.
		writeResponse(w, Response{Type: ResponseChunk, Content: raw.String()})
		writeResponse(w, Response{Type: ResponseDone})
		return
	}
	if len(sug.Commands) == 0 {
		// Valid JSON but no command: treat the explanation as a normal answer.
		writeResponse(w, Response{Type: ResponseChunk, Content: sug.Explanation})
		writeResponse(w, Response{Type: ResponseDone})
		return
	}

	sug.Risk = execution.Classify(sug.Commands, sug.Risk, s.cfg.Execution.RiskRules)
	s.sessions.SetPendingSuggestion(req.SessionID, &sug)

	writeResponse(w, Response{
		Type:        ResponseSuggestion,
		Explanation: sug.Explanation,
		Commands:    sug.Commands,
		Risk:        sug.Risk,
	})

	nc, ok := w.(net.Conn)
	if !ok {
		writeResponse(w, Response{Type: ResponseError, Error: "internal error: connection does not support reading"})
		return
	}

	var confirmReq Request
	if err := json.NewDecoder(bufio.NewReader(nc)).Decode(&confirmReq); err != nil {
		writeResponse(w, Response{Type: ResponseError, Error: err.Error()})
		return
	}

	s.sessions.ClearPendingSuggestion(req.SessionID)

	if confirmReq.Type != RequestExecuteConfirm || !confirmReq.Approve {
		writeResponse(w, Response{Type: ResponseDone})
		return
	}

	var b strings.Builder
	for _, cmd := range sug.Commands {
		out, err := execution.Execute(ctx, cmd, sug.Risk, s.cfg.Execution)
		fmt.Fprintf(&b, "$ %s\n%s", cmd, out)
		if err != nil {
			fmt.Fprintf(&b, "error: %v\n", err)
		}
	}
	writeResponse(w, Response{Type: ResponseChunk, Content: b.String()})
	writeResponse(w, Response{Type: ResponseDone})
}

// handleExecuteConfirm runs the pending suggestion once the client confirms.
// Confirmation and execution are gated on execution.enabled, and the pending
// suggestion is cleared so it can never be run a second time.
func (s *Server) handleExecuteConfirm(ctx context.Context, w io.Writer, req Request) {
	sug := s.sessions.PendingSuggestion(req.SessionID)
	s.sessions.ClearPendingSuggestion(req.SessionID)

	if sug == nil {
		writeResponse(w, Response{Type: ResponseError, Error: "no pending suggestion to confirm"})
		return
	}
	if !req.Approve || !s.cfg.Execution.Enabled {
		writeResponse(w, Response{Type: ResponseDone})
		return
	}

	var b strings.Builder
	for _, cmd := range sug.Commands {
		out, err := execution.Execute(ctx, cmd, sug.Risk, s.cfg.Execution)
		fmt.Fprintf(&b, "$ %s\n%s", cmd, out)
		if err != nil {
			fmt.Fprintf(&b, "error: %v\n", err)
		}
	}
	writeResponse(w, Response{Type: ResponseChunk, Content: b.String()})
	writeResponse(w, Response{Type: ResponseDone})
}

func parseSuggestion(raw string) (execution.Suggestion, error) {
	trimmed := strings.TrimSpace(raw)
	if start, end := strings.Index(trimmed, "{"), strings.LastIndex(trimmed, "}"); start >= 0 && end > start {
		trimmed = trimmed[start : end+1]
	}
	var sug execution.Suggestion
	if err := json.Unmarshal([]byte(trimmed), &sug); err != nil {
		return sug, err
	}
	return sug, nil
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
	models := s.modelProfiles()
	for _, profile := range sortedKeys(models) {
		model := models[profile]
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

// handleModelAdd registers a new profile->model mapping. It persists the change
// to the config file (surgically, touching only ollama.models) and updates the
// running daemon's in-memory map so the profile is usable immediately without a
// restart. The model tag need not be installed yet; the response reports its
// install status so the client can hint at `ollama pull`.
func (s *Server) handleModelAdd(ctx context.Context, w io.Writer, req Request) {
	profile := strings.TrimSpace(req.Profile)
	model := strings.TrimSpace(req.Model)
	if profile == "" || model == "" {
		writeResponse(w, Response{Type: ResponseError, Error: "usage: ai model-add <profile> <model-tag>"})
		return
	}
	if profile == "reset" {
		writeResponse(w, Response{Type: ResponseError, Error: `"reset" is reserved and cannot be used as a profile name`})
		return
	}

	path, err := s.configPath()
	if err != nil {
		writeResponse(w, Response{Type: ResponseError, Error: err.Error()})
		return
	}

	if err := config.AddProfile(path, profile, model); err != nil {
		writeResponse(w, Response{Type: ResponseError, Error: err.Error()})
		return
	}

	// Reflect the change in the running daemon immediately.
	s.mu.Lock()
	if s.cfg.Ollama.Models == nil {
		s.cfg.Ollama.Models = make(map[string]string)
	}
	s.cfg.Ollama.Models[profile] = model
	s.mu.Unlock()

	added := ModelProfile{Profile: profile, Model: model}
	if installed, err := s.installedModels(ctx); err == nil {
		added.Installed = installed[model]
	} else {
		// The profile was saved; we just can't confirm install state. Report
		// it as installed so the client doesn't emit a misleading pull hint.
		s.logger.Printf("model-add: could not verify install status for %q: %v", model, err)
		added.Installed = true
	}

	writeResponse(w, Response{
		Type:     ResponseModelAdd,
		Profiles: []ModelProfile{added},
	})
}

// handleModelRemove deletes a profile->model mapping. It refuses to remove the
// default profile (which would leave the daemon with no fallback model) and
// requires the profile to exist. The change is persisted (surgically) and
// applied to the running daemon immediately. Existing sessions that switched to
// this profile are unaffected because they store the resolved model tag, not the
// profile name.
func (s *Server) handleModelRemove(w io.Writer, req Request) {
	profile := strings.TrimSpace(req.Profile)
	if profile == "" {
		writeResponse(w, Response{Type: ResponseError, Error: "usage: ai model-rm <profile>"})
		return
	}

	s.mu.RLock()
	_, exists := s.cfg.Ollama.Models[profile]
	isDefault := s.cfg.Ollama.DefaultProfile == profile
	s.mu.RUnlock()

	if !exists {
		writeResponse(w, Response{Type: ResponseError, Error: fmt.Sprintf("profile %q is not defined", profile)})
		return
	}
	if isDefault {
		writeResponse(w, Response{Type: ResponseError, Error: fmt.Sprintf("cannot remove %q: it is the default profile (ollama.default_profile)", profile)})
		return
	}

	path, err := s.configPath()
	if err != nil {
		writeResponse(w, Response{Type: ResponseError, Error: err.Error()})
		return
	}

	if err := config.RemoveProfile(path, profile); err != nil {
		writeResponse(w, Response{Type: ResponseError, Error: err.Error()})
		return
	}

	s.mu.Lock()
	delete(s.cfg.Ollama.Models, profile)
	s.mu.Unlock()

	writeResponse(w, Response{
		Type:     ResponseModelRemove,
		Profiles: []ModelProfile{{Profile: profile}},
	})
}

// configPath returns the path of the config file this daemon uses, falling back
// to the default location when the loaded config had no backing file.
func (s *Server) configPath() (string, error) {
	if path := s.cfg.FilePath(); path != "" {
		return path, nil
	}
	return config.Path()
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
	s.mu.RLock()
	profile := s.cfg.Ollama.DefaultProfile
	model, ok := s.cfg.Ollama.Models[profile]
	s.mu.RUnlock()

	if profile == "" {
		return "", errors.New("ollama.default_profile is empty")
	}
	if !ok || model == "" {
		return "", fmt.Errorf("ollama.default_profile %q is not defined in ollama.models", profile)
	}
	return model, nil
}

func (s *Server) resolveModel(ctx context.Context, profileOrTag string) (string, error) {
	if profileOrTag == "" {
		return "", errors.New("model is required")
	}
	if model, ok := s.modelForProfile(profileOrTag); ok {
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

// modelForProfile returns the model tag mapped to a profile name, if any.
func (s *Server) modelForProfile(profile string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	model, ok := s.cfg.Ollama.Models[profile]
	return model, ok
}

// modelProfiles returns a snapshot copy of the profile->model map so callers can
// iterate it without holding the lock.
func (s *Server) modelProfiles() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string, len(s.cfg.Ollama.Models))
	for profile, model := range s.cfg.Ollama.Models {
		out[profile] = model
	}
	return out
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
