package mqtt

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/log/v2"
	pahomqtt "github.com/eclipse/paho.mqtt.golang"
)

// MessageAction is the action of a MessageCommand.
type MessageAction string

const (
	MessageShow  MessageAction = "show"
	MessageClear MessageAction = "clear"
)

const (
	// messageTextMax is Home Assistant's limit for text entities and states.
	messageTextMax = 255
	// messageMaxLength caps text sent as a raw command.
	messageMaxLength = 1000

	messageDefaultMinutes = 5.0
	messageMaxMinutes     = 1440.0

	messageDefaultPosition = "bottom"
	messageDefaultSize     = "medium"
)

var (
	messagePositions = []string{"bottom", "top", "center"}
	messageSizes     = []string{"small", "medium", "large"}
)

// MessageCommand is a text message instruction received via MQTT.
type MessageCommand struct {
	Action   MessageAction
	Target   string // client name, or "" for all clients
	Text     string
	Duration time.Duration
	Position string
	Size     string
}

// messageDraft is the text and duration set in Home Assistant for one target,
// used when the "Show message" button is pressed.
type messageDraft struct {
	text    string
	minutes float64
}

// messageJSON is the JSON form of a raw message command.
type messageJSON struct {
	Text     string  `json:"text"`
	Duration float64 `json:"duration"` // minutes
	Position string  `json:"position"`
	Size     string  `json:"size"`
}

type haTextConfig struct {
	Name              string   `json:"name"`
	UniqueID          string   `json:"unique_id"`
	CommandTopic      string   `json:"command_topic"`
	StateTopic        string   `json:"state_topic"`
	Max               int      `json:"max"`
	Mode              string   `json:"mode"`
	Icon              string   `json:"icon,omitempty"`
	AvailabilityTopic string   `json:"availability_topic"`
	Device            haDevice `json:"device"`
}

type haNumberConfig struct {
	Name              string   `json:"name"`
	UniqueID          string   `json:"unique_id"`
	CommandTopic      string   `json:"command_topic"`
	StateTopic        string   `json:"state_topic"`
	Min               float64  `json:"min"`
	Max               float64  `json:"max"`
	Step              float64  `json:"step"`
	Mode              string   `json:"mode"`
	UnitOfMeasurement string   `json:"unit_of_measurement"`
	Icon              string   `json:"icon,omitempty"`
	AvailabilityTopic string   `json:"availability_topic"`
	Device            haDevice `json:"device"`
}

// EnableMessages connects the message store. handle is called for every valid
// message command.
func (c *Client) EnableMessages(handle func(cmd MessageCommand)) {
	c.messageMu.Lock()
	c.messageHandle = handle
	c.messageMu.Unlock()
}

// messageTopic builds <prefix>[/<target>]/message/<leaf>.
func (c *Client) messageTopic(target, leaf string) string {
	if target == "" {
		return fmt.Sprintf("%s/message/%s", c.topicPrefix, leaf)
	}
	return fmt.Sprintf("%s/%s/message/%s", c.topicPrefix, target, leaf)
}

func (c *Client) subscribeMessages(client pahomqtt.Client) {
	p := c.topicPrefix
	c.subscribe(client, p+"/message/command", c.messageCommandHandler)
	c.subscribe(client, p+"/+/message/command", c.messageCommandHandler)
	c.subscribe(client, p+"/message/+/set", c.messageSetHandler)
	c.subscribe(client, p+"/+/message/+/set", c.messageSetHandler)
	// Retained states restore the drafts after a restart.
	c.subscribe(client, p+"/message/+/state", c.messageStateHandler)
	c.subscribe(client, p+"/+/message/+/state", c.messageStateHandler)
}

// publishMessageDiscovery publishes the message text, duration and buttons on
// target's device ("" = the all-clients device).
func (c *Client) publishMessageDiscovery(client pahomqtt.Client, target string) {
	deviceID, device := c.device(target)

	c.publishJSON(client, fmt.Sprintf("homeassistant/text/%s-message/config", deviceID), haTextConfig{
		Name:              "Message text",
		UniqueID:          deviceID + "-message-text",
		CommandTopic:      c.messageTopic(target, "text/set"),
		StateTopic:        c.messageTopic(target, "text/state"),
		Max:               messageTextMax,
		Mode:              "text",
		Icon:              "mdi:message-text",
		AvailabilityTopic: c.availTopic,
		Device:            device,
	})

	c.publishJSON(client, fmt.Sprintf("homeassistant/number/%s-message-duration/config", deviceID), haNumberConfig{
		Name:              "Message duration",
		UniqueID:          deviceID + "-message-duration",
		CommandTopic:      c.messageTopic(target, "duration/set"),
		StateTopic:        c.messageTopic(target, "duration/state"),
		Min:               1,
		Max:               messageMaxMinutes,
		Step:              1,
		Mode:              "box",
		UnitOfMeasurement: "min",
		Icon:              "mdi:timer-outline",
		AvailabilityTopic: c.availTopic,
		Device:            device,
	})

	buttons := []struct{ name, payload, icon string }{
		{"Show message", string(MessageShow), "mdi:message-arrow-right"},
		{"Clear message", string(MessageClear), "mdi:message-off"},
	}
	for _, b := range buttons {
		c.publishJSON(client, fmt.Sprintf("homeassistant/button/%s/message-%s/config", deviceID, b.payload), haButtonConfig{
			Name:              b.name,
			UniqueID:          deviceID + "-message-" + b.payload,
			CommandTopic:      c.messageTopic(target, "command"),
			PayloadPress:      b.payload,
			Icon:              b.icon,
			AvailabilityTopic: c.availTopic,
			Device:            device,
		})
	}
}

// messageTarget extracts the target and the field below "message" from a
// topic: message/<rest...> or <client>/message/<rest...>.
func (c *Client) messageTarget(topic string, restLen int) (target string, rest []string, ok bool) {
	parts := c.topicParts(topic)
	switch {
	case len(parts) == restLen+1 && parts[0] == "message":
		return "", parts[1:], true
	case len(parts) == restLen+2 && parts[1] == "message" && isValidClientName(parts[0]):
		return parts[0], parts[2:], true
	}
	return "", nil, false
}

// messageCommandHandler processes …/message/command messages:
//
//	show                    show the drafted text for the drafted duration
//	clear                   remove the message
//	{"text": "...", ...}    JSON with text, duration (minutes), position, size
//	any other text          show it for the drafted duration
func (c *Client) messageCommandHandler(_ pahomqtt.Client, msg pahomqtt.Message) {
	target, _, ok := c.messageTarget(msg.Topic(), 1)
	if !ok {
		return
	}

	payload := strings.TrimSpace(string(msg.Payload()))
	draft := c.draft(target)
	cmd := MessageCommand{
		Action:   MessageShow,
		Target:   target,
		Position: messageDefaultPosition,
		Size:     messageDefaultSize,
		Duration: minutesToDuration(draft.minutes),
	}

	switch {
	case payload == "":
		return
	case strings.EqualFold(payload, string(MessageShow)):
		cmd.Text = draft.text
	case strings.EqualFold(payload, string(MessageClear)):
		c.dispatchMessage(MessageCommand{Action: MessageClear, Target: target})
		return
	case strings.HasPrefix(payload, "{"):
		var j messageJSON
		if err := json.Unmarshal([]byte(payload), &j); err != nil {
			log.Warn("MQTT message: invalid JSON", "err", err)
			return
		}
		cmd.Text = j.Text
		if j.Duration > 0 {
			cmd.Duration = minutesToDuration(j.Duration)
		}
		cmd.Position = pick(j.Position, messagePositions, messageDefaultPosition)
		cmd.Size = pick(j.Size, messageSizes, messageDefaultSize)
	default:
		cmd.Text = payload
	}

	cmd.Text = normalizeMessageText(cmd.Text)
	if cmd.Text == "" {
		log.Warn("MQTT message: no text to show", "target", target)
		return
	}
	c.dispatchMessage(cmd)
}

// messageSetHandler processes HA input on …/message/text/set and …/message/duration/set.
func (c *Client) messageSetHandler(_ pahomqtt.Client, msg pahomqtt.Message) {
	target, rest, ok := c.messageTarget(msg.Topic(), 2)
	if !ok || rest[1] != "set" {
		return
	}
	if c.updateDraft(target, rest[0], string(msg.Payload())) {
		c.client.Publish(c.messageTopic(target, rest[0]+"/state"), 1, true, msg.Payload())
	}
}

// messageStateHandler restores drafts from retained states.
func (c *Client) messageStateHandler(_ pahomqtt.Client, msg pahomqtt.Message) {
	target, rest, ok := c.messageTarget(msg.Topic(), 2)
	if !ok || rest[1] != "state" {
		return
	}
	c.updateDraft(target, rest[0], string(msg.Payload()))
}

// updateDraft stores a text or duration value and reports whether it was valid.
func (c *Client) updateDraft(target, field, value string) bool {
	c.messageMu.Lock()
	defer c.messageMu.Unlock()

	d, ok := c.messageDrafts[target]
	if !ok {
		d = messageDraft{minutes: messageDefaultMinutes}
	}

	switch field {
	case "text":
		d.text = value
	case "duration":
		m, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil || m < 1 {
			log.Warn("MQTT message: invalid duration", "value", value)
			return false
		}
		d.minutes = min(m, messageMaxMinutes)
	default:
		return false
	}

	if c.messageDrafts == nil {
		c.messageDrafts = make(map[string]messageDraft)
	}
	c.messageDrafts[target] = d
	return true
}

func (c *Client) draft(target string) messageDraft {
	c.messageMu.RLock()
	defer c.messageMu.RUnlock()
	if d, ok := c.messageDrafts[target]; ok {
		return d
	}
	return messageDraft{minutes: messageDefaultMinutes}
}

func (c *Client) dispatchMessage(cmd MessageCommand) {
	c.messageMu.RLock()
	handle := c.messageHandle
	c.messageMu.RUnlock()
	if handle == nil {
		return
	}
	log.Debug("MQTT message command", "action", cmd.Action, "target", cmd.Target, "duration", cmd.Duration)
	handle(cmd)
}

// normalizeMessageText turns a literal "\n" into a line break, trims the text
// and caps its length.
func normalizeMessageText(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, `\n`, "\n"))
	if r := []rune(s); len(r) > messageMaxLength {
		s = string(r[:messageMaxLength])
	}
	return s
}

func minutesToDuration(m float64) time.Duration {
	return time.Duration(min(m, messageMaxMinutes) * float64(time.Minute))
}

// pick returns value lowercased when it is one of allowed, otherwise def.
func pick(value string, allowed []string, def string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return def
	}
	if !slices.Contains(allowed, value) {
		log.Warn("MQTT message: unknown option, using default", "value", value, "default", def)
		return def
	}
	return value
}
