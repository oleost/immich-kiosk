package mqtt

import (
	"testing"
	"time"
)

func newMessageTestClient(got *[]MessageCommand) *Client {
	c := &Client{topicPrefix: "kiosk"}
	c.EnableMessages(func(cmd MessageCommand) { *got = append(*got, cmd) })
	return c
}

func TestMessageCommandHandler(t *testing.T) {
	def := MessageCommand{Action: MessageShow, Position: "bottom", Size: "medium", Duration: 5 * time.Minute}
	with := func(f func(*MessageCommand)) MessageCommand { c := def; f(&c); return c }

	tests := []struct {
		name, topic, payload string
		want                 *MessageCommand
	}{
		{"plain text", "kiosk/message/command", "Dinner is ready",
			ptr(with(func(c *MessageCommand) { c.Text = "Dinner is ready" }))},
		{"line break", "kiosk/ipad/message/command", `Milk\nBread`,
			ptr(with(func(c *MessageCommand) { c.Text = "Milk\nBread"; c.Target = "ipad" }))},
		{"json", "kiosk/message/command", `{"text":"Hi","duration":10,"position":"TOP","size":"large"}`,
			ptr(with(func(c *MessageCommand) {
				c.Text, c.Duration, c.Position, c.Size = "Hi", 10*time.Minute, "top", "large"
			}))},
		{"json unknown options", "kiosk/message/command", `{"text":"Hi","position":"left","size":"huge"}`,
			ptr(with(func(c *MessageCommand) { c.Text = "Hi" }))},
		{"json duration capped", "kiosk/message/command", `{"text":"Hi","duration":99999}`,
			ptr(with(func(c *MessageCommand) { c.Text, c.Duration = "Hi", 1440*time.Minute }))},
		{"clear", "kiosk/ipad/message/command", "CLEAR",
			&MessageCommand{Action: MessageClear, Target: "ipad"}},
		{"show without draft text", "kiosk/message/command", "show", nil},
		{"empty json text", "kiosk/message/command", `{"text":"  "}`, nil},
		{"invalid json", "kiosk/message/command", `{"text":`, nil},
		{"reserved client name", "kiosk/override/message/command", "Hi", nil},
	}

	for _, tt := range tests {
		var got []MessageCommand
		newMessageTestClient(&got).messageCommandHandler(nil, fakeMessage{tt.topic, tt.payload})
		switch {
		case tt.want == nil && len(got) != 0:
			t.Errorf("%s: expected no command, got %+v", tt.name, got)
		case tt.want != nil && (len(got) != 1 || got[0] != *tt.want):
			t.Errorf("%s: got %+v, want %+v", tt.name, got, *tt.want)
		}
	}
}

func TestMessageShowUsesDraft(t *testing.T) {
	var got []MessageCommand
	c := newMessageTestClient(&got)

	// Retained states restore the draft without publishing.
	c.messageStateHandler(nil, fakeMessage{"kiosk/kitchen/message/text/state", "Shopping list"})
	c.messageStateHandler(nil, fakeMessage{"kiosk/kitchen/message/duration/state", "15.0"})
	c.messageStateHandler(nil, fakeMessage{"kiosk/kitchen/message/duration/state", "0"}) // ignored

	c.messageCommandHandler(nil, fakeMessage{"kiosk/kitchen/message/command", "show"})
	c.messageCommandHandler(nil, fakeMessage{"kiosk/message/command", "Hello"})

	want := []MessageCommand{
		{Action: MessageShow, Target: "kitchen", Text: "Shopping list", Duration: 15 * time.Minute, Position: "bottom", Size: "medium"},
		{Action: MessageShow, Text: "Hello", Duration: 5 * time.Minute, Position: "bottom", Size: "medium"},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestNavigationIgnoresMessageTopic(t *testing.T) {
	var calls int
	c := &Client{topicPrefix: "kiosk"}
	c.AddHandler(func(Command, string) { calls++ })

	c.messageHandler(nil, fakeMessage{"kiosk/message/command", "next"})
	if calls != 0 {
		t.Errorf("navigation handled a message topic")
	}
}

func ptr[T any](v T) *T { return &v }
