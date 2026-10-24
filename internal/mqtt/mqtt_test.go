package mqtt

import (
	"slices"
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
	c.override = &OverrideProvider{MaxDuration: 2 * time.Hour, Handle: func(cmd OverrideCommand) { got <- cmd }}
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
			OverrideCommand{Action: OverrideStart, SourceKey: "recipes", AssetName: "Lasagne Bolognese.jpg", Duration: 2 * time.Hour}, false},
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
			OverrideCommand{Action: OverrideStart, SourceKey: "recipes", AssetName: "Pizza.jpg", Duration: 2 * time.Hour}},
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

func TestOverrideDuration(t *testing.T) {
	got := make(chan OverrideCommand, 1)
	c := newTestClient(got)

	// Restored from retained states; values above the maximum are capped.
	c.overrideStateHandler(nil, fakeMessage{"kiosk/ipad/override/duration/state", "30"})
	c.overrideStateHandler(nil, fakeMessage{"kiosk/override/duration/state", "999"})

	c.overrideSelectHandler(nil, fakeMessage{"kiosk/ipad/override/recipes/set", "Pizza.jpg"})
	if cmd, _ := receive(t, got); cmd.Duration != 30*time.Minute {
		t.Errorf("ipad duration = %v, want 30m", cmd.Duration)
	}

	c.overrideCommandHandler(nil, fakeMessage{"kiosk/override/command", "start recipes"})
	if cmd, _ := receive(t, got); cmd.Duration != 2*time.Hour {
		t.Errorf("global duration = %v, want 2h (capped)", cmd.Duration)
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

func TestOverrideTargetsIncludeSeenClients(t *testing.T) {
	c := newTestClient(make(chan OverrideCommand, 1))
	c.override.Clients = func() []string { return []string{"ipad"} }

	// Retained states of clients that are not connected.
	c.overrideStateHandler(nil, fakeMessage{"kiosk/pc/override/recipes/state", "off"})
	c.overrideStateHandler(nil, fakeMessage{"kiosk/ipad/override/duration/state", "30"})
	c.overrideStateHandler(nil, fakeMessage{"kiosk/override/recipes/state", "off"})

	got := c.overrideTargets(c.override)
	if want := []string{"ipad", "pc"}; !slices.Equal(got, want) {
		t.Errorf("targets = %v, want %v", got, want)
	}
	if d := c.draftDuration("pc", fieldOverrideDuration); d != 2*time.Hour {
		t.Errorf("pc duration = %v, want the 2h default", d)
	}
}

func TestControlTarget(t *testing.T) {
	c := &Client{topicPrefix: "kiosk"}
	tests := []struct {
		topic, field, leaf string
		target             string
		ok                 bool
	}{
		{"kiosk/album/set", "album", "set", "", true},
		{"kiosk/ipad/pause/state", "pause", "state", "ipad", true},
		// The topics of an override source named "pause", not a switch.
		{"kiosk/override/pause/set", "pause", "set", "", false},
		{"kiosk/message/sleep/set", "sleep", "set", "", false},
		{"kiosk/ipad/pause/set", "sleep", "set", "", false},
		{"kiosk/a/b/pause/set", "pause", "set", "", false},
	}
	for _, tt := range tests {
		target, ok := c.controlTarget(tt.topic, tt.field, tt.leaf)
		if target != tt.target || ok != tt.ok {
			t.Errorf("controlTarget(%s) = %q, %v; want %q, %v", tt.topic, target, ok, tt.target, tt.ok)
		}
	}
}

func TestSelectStateRestore(t *testing.T) {
	var restored []string
	c := &Client{topicPrefix: "kiosk"}
	sel := SelectControl{
		Field:   "album",
		Options: func(string) ([]string, bool) { return nil, false },
		Restore: func(target, option string) bool {
			restored = append(restored, option+"@"+target)
			return true
		},
	}

	for _, m := range []fakeMessage{
		{"kiosk/album/state", "Cabin"},
		{"kiosk/ipad/album/state", "Default"},
		{"kiosk/override/album/state", "off"},
		{"kiosk/ipad/album/state", ""},
	} {
		c.controlHandler("album", "state", func(target, value string) { c.handleSelectState(sel, target, value) })(nil, m)
	}

	if want := []string{"Cabin@", "Default@ipad"}; !slices.Equal(restored, want) {
		t.Errorf("restored = %v, want %v", restored, want)
	}
}

func TestParseSwitch(t *testing.T) {
	for value, want := range map[string]bool{"ON": true, "on": true, "OFF": false} {
		if on, ok := parseSwitch(value); !ok || on != want {
			t.Errorf("parseSwitch(%q) = %v, %v", value, on, ok)
		}
	}
	if _, ok := parseSwitch("maybe"); ok {
		t.Error("parseSwitch accepted an invalid value")
	}
}

func TestNavigationReload(t *testing.T) {
	var calls []string
	c := &Client{topicPrefix: "kiosk"}
	c.AddHandler(func(cmd Command, target string) { calls = append(calls, string(cmd)+"@"+target) })

	c.messageHandler(nil, fakeMessage{"kiosk/ipad/command", "reload"})
	if want := []string{"reload@ipad"}; !slices.Equal(calls, want) {
		t.Errorf("got %v, want %v", calls, want)
	}
}
