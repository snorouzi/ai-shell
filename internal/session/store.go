package session

import (
	"sync"
	"time"

	"ai-shell/internal/execution"
)

type Store struct {
	mu         sync.Mutex
	sessions   map[string]*Session
	maxHistory int
}

func NewStore(maxHistory int) *Store {
	if maxHistory < 0 {
		maxHistory = 0
	}
	return &Store{
		sessions:   make(map[string]*Session),
		maxHistory: maxHistory,
	}
}

func (s *Store) Messages(sessionID string) []Message {
	session := s.get(sessionID)

	s.mu.Lock()
	defer s.mu.Unlock()

	messages := make([]Message, len(session.Messages))
	copy(messages, session.Messages)
	return messages
}

func (s *Store) AddMessages(sessionID string, messages ...Message) {
	session := s.get(sessionID)

	s.mu.Lock()
	defer s.mu.Unlock()

	session.Messages = append(session.Messages, messages...)
	if s.maxHistory > 0 && len(session.Messages) > s.maxHistory {
		session.Messages = session.Messages[len(session.Messages)-s.maxHistory:]
	}
	session.LastActivity = time.Now()
}

func (s *Store) CurrentModel(sessionID string) string {
	session := s.get(sessionID)

	s.mu.Lock()
	defer s.mu.Unlock()

	return session.CurrentModel
}

func (s *Store) SetCurrentModel(sessionID, model string) {
	session := s.get(sessionID)

	s.mu.Lock()
	defer s.mu.Unlock()

	session.CurrentModel = model
	session.LastActivity = time.Now()
}

func (s *Store) Touch(sessionID string) {
	session := s.get(sessionID)

	s.mu.Lock()
	defer s.mu.Unlock()

	session.LastActivity = time.Now()
}

// SetPendingSuggestion stores the suggestion awaiting the user's confirmation.
func (s *Store) SetPendingSuggestion(sessionID string, sug *execution.Suggestion) {
	session := s.get(sessionID)

	s.mu.Lock()
	defer s.mu.Unlock()

	session.PendingSuggestion = sug
	session.LastActivity = time.Now()
}

// PendingSuggestion returns the suggestion awaiting confirmation, if any.
func (s *Store) PendingSuggestion(sessionID string) *execution.Suggestion {
	session := s.get(sessionID)

	s.mu.Lock()
	defer s.mu.Unlock()

	return session.PendingSuggestion
}

// ClearPendingSuggestion drops any stored suggestion (e.g. after it is
// confirmed or rejected) so it cannot be executed twice.
func (s *Store) ClearPendingSuggestion(sessionID string) {
	session := s.get(sessionID)

	s.mu.Lock()
	defer s.mu.Unlock()

	session.PendingSuggestion = nil
}

func (s *Store) get(sessionID string) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[sessionID]
	if !ok {
		session = &Session{
			LastActivity: time.Now(),
		}
		s.sessions[sessionID] = session
	}
	return session
}
