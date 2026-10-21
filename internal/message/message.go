// Package message manages timed text messages shown on top of the slideshow.
//
// A message is set for one client or for all clients and disappears after its
// duration. Messages are kept in memory only.
package message

import (
	"context"
	"sync"
	"time"

	"charm.land/log/v2"
)

const expiryCheckInterval = 5 * time.Second

// Message is an active message for one target.
type Message struct {
	Text      string
	Position  string
	Size      string
	ExpiresAt time.Time
}

// Store holds active messages per target ("" = all clients).
type Store struct {
	mu       sync.Mutex
	messages map[string]*Message

	onExpire func(target string)
}

// New creates an empty Store.
func New() *Store {
	return &Store{messages: make(map[string]*Message)}
}

// SetOnExpire registers a callback run when a message expires.
// Must be called before Start.
func (s *Store) SetOnExpire(fn func(target string)) {
	s.onExpire = fn
}

// Start runs the expiry loop in the background.
func (s *Store) Start(ctx context.Context) {
	go func() {
		t := time.NewTicker(expiryCheckInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.expire()
			}
		}
	}()
}

// Show sets the message for target, replacing any message already shown there.
func (s *Store) Show(target string, m Message) {
	s.mu.Lock()
	s.messages[target] = &m
	s.mu.Unlock()
	log.Warn("message: shown", "target", target, "until", m.ExpiresAt.Format(time.TimeOnly))
}

// Clear removes the message for target.
func (s *Store) Clear(target string) {
	s.mu.Lock()
	delete(s.messages, target)
	s.mu.Unlock()
	log.Warn("message: cleared", "target", target)
}

// Active returns the message shown to clientName: a per-client message takes
// priority over a global one. Returns nil when none is active.
func (s *Store) Active(clientName string) *Message {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	if clientName != "" {
		if m, ok := s.messages[clientName]; ok && now.Before(m.ExpiresAt) {
			c := *m
			return &c
		}
	}
	if m, ok := s.messages[""]; ok && now.Before(m.ExpiresAt) {
		c := *m
		return &c
	}
	return nil
}

func (s *Store) expire() {
	now := time.Now()
	var expired []string

	s.mu.Lock()
	for target, m := range s.messages {
		if !now.Before(m.ExpiresAt) {
			expired = append(expired, target)
			delete(s.messages, target)
		}
	}
	s.mu.Unlock()

	for _, target := range expired {
		log.Warn("message: expired", "target", target)
		if s.onExpire != nil {
			s.onExpire(target)
		}
	}
}
