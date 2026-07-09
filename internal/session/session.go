package session

import (
	"time"
)

type Message struct {
	Role    string
	Content string
}

type Session struct {
	Messages     []Message
	CurrentModel string
	LastActivity time.Time
}
