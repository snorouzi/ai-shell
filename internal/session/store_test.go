package session

import "testing"

func TestStoreTruncatesHistory(t *testing.T) {
	store := NewStore(2)

	store.AddMessages("s1",
		Message{Role: "user", Content: "one"},
		Message{Role: "assistant", Content: "two"},
		Message{Role: "user", Content: "three"},
	)

	messages := store.Messages("s1")
	if len(messages) != 2 {
		t.Fatalf("len(messages) = %d, want 2", len(messages))
	}
	if messages[0].Content != "two" || messages[1].Content != "three" {
		t.Fatalf("messages = %#v", messages)
	}
}

func TestStoreTracksCurrentModel(t *testing.T) {
	store := NewStore(20)

	store.SetCurrentModel("s1", "qwen3:4b")

	if got := store.CurrentModel("s1"); got != "qwen3:4b" {
		t.Fatalf("CurrentModel = %q", got)
	}
}
