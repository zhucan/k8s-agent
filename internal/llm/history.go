package llm

import (
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

const (
	maxHistoryTurns = 10
	historyTTL      = 2 * time.Hour
)

type session struct {
	messages []anthropic.MessageParam
	updated  time.Time
}

// historyStore keeps the text-only conversation per session key. Tool-use blocks are
// not stored so truncation never splits a tool_use/tool_result pair.
type historyStore struct {
	mu       sync.Mutex
	sessions map[string]*session
	maxTurns int
	ttl      time.Duration
	now      func() time.Time
}

func newHistoryStore() *historyStore {
	return &historyStore{
		sessions: make(map[string]*session),
		maxTurns: maxHistoryTurns,
		ttl:      historyTTL,
		now:      time.Now,
	}
}

func (h *historyStore) get(key string) []anthropic.MessageParam {
	if key == "" {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.sessions[key]
	if !ok {
		return nil
	}
	if h.now().Sub(s.updated) > h.ttl {
		delete(h.sessions, key)
		return nil
	}
	return append([]anthropic.MessageParam(nil), s.messages...)
}

func (h *historyStore) append(key, userText, assistantText string) {
	if key == "" || assistantText == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	for k, s := range h.sessions {
		if now.Sub(s.updated) > h.ttl {
			delete(h.sessions, k)
		}
	}
	s, ok := h.sessions[key]
	if !ok {
		s = &session{}
		h.sessions[key] = s
	}
	s.messages = append(s.messages,
		anthropic.NewUserMessage(anthropic.NewTextBlock(userText)),
		anthropic.NewAssistantMessage(anthropic.NewTextBlock(assistantText)),
	)
	if limit := h.maxTurns * 2; len(s.messages) > limit {
		s.messages = append([]anthropic.MessageParam(nil), s.messages[len(s.messages)-limit:]...)
	}
	s.updated = now
}
