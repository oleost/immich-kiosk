package mqtt

import (
	"testing"
	"time"
)

type fakeMessage struct{ topic, payload string }

func (m fakeMessage) Duplicate() bool   { return false }
func (m fakeMessage) Qos() byte         { return 1 }
func (m fakeMessage) Retained() bool    { return false }
func (m fakeMessage) Topic() string     { return m.topic }
func (m fakeMessage) MessageID() uint16 { return 0 }
func (m fakeMessage) Payload() []byte   { return []byte(m.payload) }
func (m fakeMessage) Ack()              {}

func newTestClient(got chan OverrideCommand) *Client {
	c := &Client{topicPrefix: "kiosk"}
	c.override = &OverrideProvider{Handle: func(cmd OverrideCommand) { got <- cmd }}
	return c
}

func receive(t *testing.T, got chan OverrideCommand) (OverrideCommand, bool) {
	t.Helper()
	select {
	case cmd := <-got:
		return cmd, true
	case <-time.After(200 * time.Millisecond):
		return OverrideCommand{}, false
	}
}

func TestOverrideCommandHandler(t *testing.T) {
	tests := []struct {
		topic, payload string
		want           OverrideCommand
		wantNone       bool
	}{
		{"kiosk/override/command", "start recipes Lasagne Bolognese.jpg",
			OverrideCommand{Action: OverrideStart, SourceKey: "recipes", AssetName: "Lasagne Bolognese.jpg"}, false},
		{"kiosk/ipad/override/command", "STOP",
			OverrideCommand{Action: OverrideStop, Target: "ipad"}, false},
		{"kiosk/override/command", "refresh-sources recipes",
			OverrideCommand{Action: OverrideRefresh, SourceKey: "recipes"}, false},
		{"kiosk/override/command", "start", OverrideCommand{}, true},
		{"kiosk/override/command", "dance", OverrideCommand{}, true},
	}

	for _, tt := range tests {
		got := make(chan OverrideCommand, 1)
		newTestClient(got).overrideCommandHandler(nil, fakeMessage{tt.topic, tt.payload})
		cmd, ok := receive(t, got)
		if tt.wantNone {
			if ok {
				t.Errorf("%s %q: expected no command, got %+v", tt.topic, tt.payload, cmd)
			}
			continue
		}
		if !ok || cmd != tt.want {
			t.Errorf("%s %q: got %+v, want %+v", tt.topic, tt.payload, cmd, tt.want)
		}
	}
}

func TestOverrideSelectHandler(t *testing.T) {
	tests := []struct {
		topic, payload string
		want           OverrideCommand
	}{
		{"kiosk/override/recipes/set", "Pizza.jpg",
			OverrideCommand{Action: OverrideStart, SourceKey: "recipes", AssetName: "Pizza.jpg"}},
		{"kiosk/ipad/override/recipes/set", "off",
			OverrideCommand{Action: OverrideStop, SourceKey: "recipes", Target: "ipad"}},
	}

	for _, tt := range tests {
		got := make(chan OverrideCommand, 1)
		newTestClient(got).overrideSelectHandler(nil, fakeMessage{tt.topic, tt.payload})
		if cmd, ok := receive(t, got); !ok || cmd != tt.want {
			t.Errorf("%s %q: got %+v, want %+v", tt.topic, tt.payload, cmd, tt.want)
		}
	}
}

func TestNavigationIgnoresOverrideTopic(t *testing.T) {
	var calls []string
	c := &Client{topicPrefix: "kiosk"}
	c.AddHandler(func(cmd Command, target string) { calls = append(calls, string(cmd)+"@"+target) })

	c.messageHandler(nil, fakeMessage{"kiosk/override/command", "next"})
	c.messageHandler(nil, fakeMessage{"kiosk/ipad/command", "next"})
	c.messageHandler(nil, fakeMessage{"kiosk/command", "previous"})

	want := []string{"next@ipad", "previous@"}
	if len(calls) != len(want) || calls[0] != want[0] || calls[1] != want[1] {
		t.Errorf("got %v, want %v", calls, want)
	}
}
