package routes

import (
	"context"
	"encoding/json"
	"math"
	"time"

	"charm.land/log/v2"

	"github.com/damongolding/immich-kiosk/internal/message"
	"github.com/damongolding/immich-kiosk/internal/mqtt"
)

// messageStore holds active text messages; nil when MQTT is not connected.
var messageStore *message.Store

// messagePayload is the data of a kiosk-message SSE event. An empty Text
// means no message is shown.
type messagePayload struct {
	Text      string `json:"text"`
	Position  string `json:"position,omitempty"`
	Size      string `json:"size,omitempty"`
	Remaining int    `json:"remaining,omitempty"` // seconds
}

// SetupMessages creates the message store and connects it to MQTT.
func SetupMessages(ctx context.Context, mqttClient *mqtt.Client) {
	store := message.New()
	messageStore = store

	store.SetOnExpire(pushMessage)
	mqttClient.EnableMessages(func(cmd mqtt.MessageCommand) {
		switch cmd.Action {
		case mqtt.MessageShow:
			store.Show(cmd.Target, message.Message{
				Text:      cmd.Text,
				Position:  cmd.Position,
				Size:      cmd.Size,
				ExpiresAt: time.Now().Add(cmd.Duration),
			})
		case mqtt.MessageClear:
			store.Clear(cmd.Target)
		}
		pushMessage(cmd.Target)
	})

	store.Start(ctx)
}

// pushMessage sends every client of target ("" = all clients) the message it
// should show now. A client-specific message wins over a global one.
func pushMessage(target string) {
	sseHub.sendEach(target, func(clientName string) sseEvent {
		ev, _ := messageEvent(clientName)
		return ev
	})
}

// messageEvent builds the kiosk-message event for clientName and reports
// whether a message is active.
func messageEvent(clientName string) (sseEvent, bool) {
	var p messagePayload
	active := false

	if messageStore != nil {
		if clientName == "_global" {
			clientName = ""
		}
		if m := messageStore.Active(clientName); m != nil {
			active = true
			p = messagePayload{
				Text:      m.Text,
				Position:  m.Position,
				Size:      m.Size,
				Remaining: int(math.Ceil(time.Until(m.ExpiresAt).Seconds())),
			}
		}
	}

	data, err := json.Marshal(p)
	if err != nil {
		log.Error("message: marshal event", "err", err)
	}
	return sseEvent{name: sseEventMessage, data: string(data)}, active
}
