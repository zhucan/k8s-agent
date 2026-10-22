package llm

import (
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

func TestHistoryStore_RoundTrip(t *testing.T) {
	h := newHistoryStore()
	if got := h.get("t1"); got != nil {
		t.Fatalf("expected empty history, got %d messages", len(got))
	}

	h.append("t1", "q1", "a1")
	h.append("t1", "q2", "a2")

	got := h.get("t1")
	if len(got) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(got))
	}
	if textOf(got[0]) != "q1" || textOf(got[3]) != "a2" {
		t.Fatalf("unexpected message order: %q ... %q", textOf(got[0]), textOf(got[3]))
	}
	if other := h.get("t2"); other != nil {
		t.Fatalf("sessions leaked across keys")
	}
}

func TestHistoryStore_TruncatesToMaxTurns(t *testing.T) {
	h := newHistoryStore()
	h.maxTurns = 2
	for _, turn := range [][2]string{{"q1", "a1"}, {"q2", "a2"}, {"q3", "a3"}} {
		h.append("t", turn[0], turn[1])
	}
	got := h.get("t")
	if len(got) != 4 {
		t.Fatalf("expected 4 messages kept, got %d", len(got))
	}
	if textOf(got[0]) != "q2" || textOf(got[3]) != "a3" {
		t.Fatalf("oldest turn not dropped: %q ... %q", textOf(got[0]), textOf(got[3]))
	}
}

func TestHistoryStore_Expires(t *testing.T) {
	h := newHistoryStore()
	now := time.Now()
	h.now = func() time.Time { return now }
	h.append("t", "q1", "a1")

	now = now.Add(historyTTL + time.Minute)
	if got := h.get("t"); got != nil {
		t.Fatalf("expected expired history to be dropped, got %d messages", len(got))
	}
}

func TestHistoryStore_EmptyKeySkipped(t *testing.T) {
	h := newHistoryStore()
	h.append("", "q", "a")
	if got := h.get(""); got != nil {
		t.Fatalf("empty key should not be stored, got %d messages", len(got))
	}
}

func textOf(m anthropic.MessageParam) string {
	return m.Content[0].OfText.Text
}
