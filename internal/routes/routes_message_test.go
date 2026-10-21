package routes

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/damongolding/immich-kiosk/internal/message"
)

func TestPushMessage(t *testing.T) {
	old := messageStore
	messageStore = message.New()
	t.Cleanup(func() { messageStore = old })

	kitchen := sseHub.subscribe("kitchen")
	defer sseHub.unsubscribe("kitchen", kitchen)
	unnamed := sseHub.subscribe("_global")
	defer sseHub.unsubscribe("_global", unnamed)

	next := func(ch chan sseEvent) messagePayload {
		t.Helper()
		select {
		case ev := <-ch:
			if ev.name != sseEventMessage {
				t.Fatalf("event name = %q", ev.name)
			}
			var p messagePayload
			if err := json.Unmarshal([]byte(ev.data), &p); err != nil {
				t.Fatal(err)
			}
			return p
		case <-time.After(time.Second):
			t.Fatal("no event")
			return messagePayload{}
		}
	}

	messageStore.Show("kitchen", message.Message{Text: "dinner", Position: "top", Size: "large", ExpiresAt: time.Now().Add(time.Minute)})
	pushMessage("kitchen")
	if p := next(kitchen); p.Text != "dinner" || p.Position != "top" || p.Remaining < 59 {
		t.Errorf("kitchen got %+v", p)
	}

	// A global message reaches every client, but kitchen keeps its own.
	messageStore.Show("", message.Message{Text: "hello all", ExpiresAt: time.Now().Add(time.Minute)})
	pushMessage("")
	if p := next(kitchen); p.Text != "dinner" {
		t.Errorf("kitchen got %+v, want its own message", p)
	}
	if p := next(unnamed); p.Text != "hello all" {
		t.Errorf("unnamed client got %+v", p)
	}

	messageStore.Clear("kitchen")
	pushMessage("kitchen")
	if p := next(kitchen); p.Text != "hello all" {
		t.Errorf("after clear kitchen got %+v, want global message", p)
	}

	messageStore.Clear("")
	pushMessage("")
	if p := next(unnamed); p.Text != "" {
		t.Errorf("after clearing all, unnamed got %+v", p)
	}
}
