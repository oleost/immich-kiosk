// Package switches holds on/off controls set from Home Assistant, such as
// pausing the slideshow, for all clients ("") or one named client.
//
// A switch that is on for all clients applies to every client; a client's own
// switch can turn it on for that client, but not off.
package switches

import "sync"

type key struct {
	target string
	field  string
}

// Store holds the switch states.
type Store struct {
	mu     sync.Mutex
	states map[key]bool
}

// New creates an empty Store; every switch starts off.
func New() *Store {
	return &Store{states: make(map[key]bool)}
}

// Get returns the switch of exactly target.
func (s *Store) Get(target, field string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.states[key{target, field}]
}

// Set turns the switch of target on or off.
func (s *Store) Set(target, field string, on bool) {
	s.mu.Lock()
	s.states[key{target, field}] = on
	s.mu.Unlock()
}

// Restore sets the switch of target from a retained state, unless it was
// already set since startup. It reports whether the state was applied.
func (s *Store) Restore(target, field string, on bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.states[key{target, field}]; ok {
		return false
	}
	s.states[key{target, field}] = on
	return true
}

// Active reports whether the switch applies to clientName: it is on for all
// clients or for that client.
func (s *Store) Active(clientName, field string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.states[key{"", field}] || (clientName != "" && s.states[key{clientName, field}])
}
