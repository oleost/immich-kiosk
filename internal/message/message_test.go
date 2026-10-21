package message

import (
	"testing"
	"time"
)

func TestActivePriorityAndExpiry(t *testing.T) {
	s := New()
	var expired []string
	s.SetOnExpire(func(target string) { expired = append(expired, target) })

	s.Show("", Message{Text: "all", ExpiresAt: time.Now().Add(time.Hour)})
	s.Show("kitchen", Message{Text: "kitchen", ExpiresAt: time.Now().Add(-time.Second)})

	if m := s.Active("kitchen"); m == nil || m.Text != "all" {
		t.Errorf("expired client message should fall back to global, got %+v", m)
	}

	s.Show("kitchen", Message{Text: "kitchen", ExpiresAt: time.Now().Add(time.Hour)})
	if m := s.Active("kitchen"); m == nil || m.Text != "kitchen" {
		t.Errorf("client message should win, got %+v", m)
	}
	if m := s.Active("hall"); m == nil || m.Text != "all" {
		t.Errorf("other clients should get the global message, got %+v", m)
	}

	s.Clear("")
	if m := s.Active("hall"); m != nil {
		t.Errorf("cleared global message still active: %+v", m)
	}

	s.Show("hall", Message{Text: "old", ExpiresAt: time.Now().Add(-time.Second)})
	s.expire()
	if len(expired) != 1 || expired[0] != "hall" {
		t.Errorf("expired = %v, want [hall]", expired)
	}
}
