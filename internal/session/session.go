package session

import (
	"time"

	"ai-shell/internal/execution"
)

type Message struct {
	Role    string
	Content string
}

type Session struct {
	Messages          []Message
	CurrentModel      string
	LastActivity      time.Time
	PendingSuggestion *execution.Suggestion
}
