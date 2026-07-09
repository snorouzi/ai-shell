package session

import (
	"sync"
	"time"
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
