// Package mqtt provides MQTT client support for remote control of Immich Kiosk.
//
// On connect it publishes Home Assistant MQTT Discovery messages so the kiosk
// appears automatically in Home Assistant. Each named client (e.g. ?client=living-room)
// gets its own HA device, in addition to a global device that controls all
// connected clients at once.
//
// MQTT topics:
//
//	<prefix>/command                          – navigation command to all clients
//	<prefix>/<client>/command                 – navigation command to one client
//	<prefix>/override/command                 – override command to all clients
//	<prefix>/<client>/override/command        – override command to one client
//	<prefix>/override/<source>/set            – HA select input (all clients)
//	<prefix>/<client>/override/<source>/set   – HA select input (one client)
//	<prefix>/override/<source>/state          – HA select state (all clients)
//	<prefix>/<client>/override/<source>/state – HA select state (one client)
//	<prefix>[/<client>]/override/duration/set – HA number input (minutes)
//	<prefix>[/<client>]/message/command       – text message command
//	<prefix>[/<client>]/message/text/set      – HA text input (message text)
//	<prefix>[/<client>]/message/duration/set  – HA number input (minutes)
//	<prefix>[/<client>]/message/<field>/state – HA text/number state
//	<prefix>[/<client>]/<control>/set         – HA select/switch input (album, person,
//	                                            pause, sleep, favorites, memories)
//	<prefix>[/<client>]/<control>/state       – HA select/switch state
//	<prefix>[/<client>]/image/…               – what the screen shows (sensor, image)
//	<prefix>/status                           – availability (online/offline)
package mqtt

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"charm.land/log/v2"
	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/damongolding/immich-kiosk/internal/config"
)

// Command represents a navigation command received via MQTT.
type Command string

const (
	CommandNext     Command = "next"
	CommandPrevious Command = "previous"
	CommandReload   Command = "reload"
)

// Handler is called when a valid navigation command is received.
// target is the client name the command is directed at, or "" for all clients.
type Handler func(cmd Command, target string)

// OverrideAction is the action of an OverrideCommand.
type OverrideAction string

const (
	OverrideStart   OverrideAction = "start"
	OverrideStop    OverrideAction = "stop"
	OverrideRefresh OverrideAction = "refresh-sources"
)

// overrideOff is the select option (and state) meaning "no override".
const overrideOff = "off"

// OverrideCommand is an override instruction received via MQTT.
type OverrideCommand struct {
	Action    OverrideAction
	SourceKey string // empty for a plain "stop" or "refresh-sources"
	AssetName string // empty to show the whole album
	Target    string // client name, or "" for all clients
	// Duration is how long a started override lasts, as set in Home Assistant.
	Duration time.Duration
}

// overrideDurationKey is the topic segment of the override duration number. It
// shares the override/<source> namespace, so no source may use it as its key.
const overrideDurationKey = "duration"

// OverrideProvider connects the MQTT layer to the override store.
type OverrideProvider struct {
	Sources []config.OverrideSource
	// MaxDuration is the default and longest override duration.
	MaxDuration time.Duration
	// AssetNames returns the selectable labels (description or filename) for a source.
	AssetNames func(sourceKey string) []string
	// Current returns the source and asset name of the override set for exactly
	// this target (no global fallback), and whether one is active.
	Current func(target string) (sourceKey, assetName string, ok bool)
	// Clients returns the names of currently connected clients.
	Clients func() []string
	// Handle executes an override command.
	Handle func(cmd OverrideCommand)
}

// Client wraps the paho MQTT client.
type Client struct {
	client      pahomqtt.Client
	topicPrefix string
	clientID    string
	availTopic  string

	handlers   []Handler
	handlersMu sync.RWMutex

	override   *OverrideProvider
	overrideMu sync.RWMutex

	controls   *controls
	controlsMu sync.RWMutex

	messageHandle func(cmd MessageCommand)
	messageMu     sync.RWMutex

	drafts   map[draftKey]string
	draftsMu sync.RWMutex

	// seenClients are clients with retained override states on the broker,
	// so their selects can be updated while they are not connected.
	seenClients map[string]struct{}
	seenMu      sync.Mutex
}

type haDevice struct {
	Identifiers  []string `json:"identifiers"`
	Name         string   `json:"name"`
	Model        string   `json:"model"`
	Manufacturer string   `json:"manufacturer"`
}

type haButtonConfig struct {
	Name              string   `json:"name"`
	UniqueID          string   `json:"unique_id"`
	CommandTopic      string   `json:"command_topic"`
	PayloadPress      string   `json:"payload_press"`
	Icon              string   `json:"icon,omitempty"`
	AvailabilityTopic string   `json:"availability_topic"`
	Device            haDevice `json:"device"`
}

type haSelectConfig struct {
	Name              string   `json:"name"`
	UniqueID          string   `json:"unique_id"`
	CommandTopic      string   `json:"command_topic"`
	StateTopic        string   `json:"state_topic"`
	Options           []string `json:"options"`
	Icon              string   `json:"icon,omitempty"`
	AvailabilityTopic string   `json:"availability_topic"`
	Device            haDevice `json:"device"`
}

// New creates and connects a new MQTT client.
func New(ctx context.Context, settings config.KioskSettings) (*Client, error) {
	broker := settings.MqttBroker
	if broker == "" {
		return nil, fmt.Errorf("mqtt_broker is not configured")
	}

	prefix := strings.TrimRight(settings.MqttTopicPrefix, "/")

	c := &Client{
		topicPrefix: prefix,
		clientID:    settings.MqttClientID,
		availTopic:  prefix + "/status",
	}

	opts := pahomqtt.NewClientOptions()
	opts.AddBroker(fmt.Sprintf("tcp://%s:%d", broker, settings.MqttPort))
	opts.SetClientID(settings.MqttClientID)
	opts.SetCleanSession(true)
	opts.SetAutoReconnect(true)
	opts.SetConnectRetry(true)
	opts.SetConnectRetryInterval(10 * time.Second)
	opts.SetKeepAlive(30 * time.Second)
	opts.SetWill(c.availTopic, "offline", 1, true)

	if settings.MqttUsername != "" {
		opts.SetUsername(settings.MqttUsername)
	}
	if settings.MqttPassword != "" {
		opts.SetPassword(settings.MqttPassword)
	}

	subscribeOverride := len(settings.OverrideSources) > 0

	opts.SetOnConnectHandler(func(client pahomqtt.Client) {
		log.Warn("MQTT connected", "broker", broker)

		client.Publish(c.availTopic, 1, true, "online")
		c.publishNavDiscovery(client, "")
		c.publishNowShowingDiscovery("")

		c.subscribe(client, prefix+"/command", c.messageHandler)
		c.subscribe(client, prefix+"/+/command", c.messageHandler)

		c.subscribeMessages(client)
		c.publishMessageDiscovery(client, "")

		// Home Assistant forgets states without a retained value (an empty
		// message text) when it restarts.
		c.subscribe(client, "homeassistant/status", func(_ pahomqtt.Client, msg pahomqtt.Message) {
			if string(msg.Payload()) == "online" {
				c.republishDrafts()
			}
		})

		if subscribeOverride {
			c.subscribe(client, prefix+"/override/command", c.overrideCommandHandler)
			c.subscribe(client, prefix+"/+/override/command", c.overrideCommandHandler)
			c.subscribe(client, prefix+"/override/+/set", c.overrideSelectHandler)
			c.subscribe(client, prefix+"/+/override/+/set", c.overrideSelectHandler)
			// Retained states restore the override duration after a restart and
			// reveal clients that are not connected right now.
			c.subscribe(client, prefix+"/override/"+overrideDurationKey+"/state", c.overrideStateHandler)
			c.subscribe(client, prefix+"/+/override/+/state", c.overrideStateHandler)
			c.publishOverrideDiscovery(client)
		}

		// On the first connect the controls are not enabled yet;
		// EnableControls subscribes then.
		c.subscribeControls(client)
	})

	opts.SetConnectionLostHandler(func(_ pahomqtt.Client, err error) {
		log.Warn("MQTT connection lost", "err", err)
	})

	opts.SetReconnectingHandler(func(_ pahomqtt.Client, _ *pahomqtt.ClientOptions) {
		log.Warn("MQTT reconnecting...")
	})

	client := pahomqtt.NewClient(opts)
	c.client = client

	token := client.Connect()
	token.Wait()
	if err := token.Error(); err != nil {
		return nil, fmt.Errorf("mqtt connect: %w", err)
	}

	go func() {
		<-ctx.Done()
		log.Warn("MQTT disconnecting")
		client.Publish(c.availTopic, 1, true, "offline").WaitTimeout(time.Second)
		client.Disconnect(500)
	}()

	return c, nil
}

func (c *Client) subscribe(client pahomqtt.Client, topic string, cb pahomqtt.MessageHandler) {
	if t := client.Subscribe(topic, 1, cb); t.Wait() && t.Error() != nil {
		log.Error("MQTT subscribe failed", "topic", topic, "err", t.Error())
		return
	}
	log.Debug("MQTT subscribed", "topic", topic)
}

// AddHandler registers a handler called when a navigation command is received.
func (c *Client) AddHandler(h Handler) {
	c.handlersMu.Lock()
	c.handlers = append(c.handlers, h)
	c.handlersMu.Unlock()
}

// EnableOverride connects the override store and publishes its HA entities.
func (c *Client) EnableOverride(p OverrideProvider) {
	p.Sources = slices.DeleteFunc(slices.Clone(p.Sources), func(src config.OverrideSource) bool {
		if src.Key == overrideDurationKey {
			log.Error("override source key is reserved, not shown in Home Assistant", "key", src.Key)
			return true
		}
		return false
	})

	c.overrideMu.Lock()
	c.override = &p
	c.overrideMu.Unlock()

	c.publishOverrideDiscovery(c.client)
}

func (c *Client) overrideProvider() *OverrideProvider {
	c.overrideMu.RLock()
	defer c.overrideMu.RUnlock()
	return c.override
}

// isValidClientName reports whether name is safe as an MQTT topic segment and
// does not collide with the reserved "override" and "message" segments.
func isValidClientName(name string) bool {
	if name == "" || name == "_global" || name == "override" || name == "message" {
		return false
	}
	return !strings.ContainsAny(name, "/#+")
}

// PublishClientDiscovery publishes HA MQTT Discovery for a named client
// (e.g. "living-room"). Called when a client connects via SSE.
func (c *Client) PublishClientDiscovery(clientName string) {
	if !isValidClientName(clientName) {
		return
	}
	c.publishNavDiscovery(c.client, clientName)
	c.publishMessageDiscovery(c.client, clientName)
	if p := c.overrideProvider(); p != nil {
		c.publishOverrideSelects(c.client, p, clientName)
	}
	c.PublishControlDiscovery(clientName)
	c.publishNowShowingDiscovery(clientName)
	log.Info("MQTT client discovery published", "client", clientName)
}

// PublishOverrideDiscovery republishes the override selects for the global
// device and every connected client, e.g. after the asset list changed.
func (c *Client) PublishOverrideDiscovery() {
	c.publishOverrideDiscovery(c.client)
}

// PublishOverrideState publishes the select state for every source of target.
func (c *Client) PublishOverrideState(target string) {
	p := c.overrideProvider()
	if p == nil {
		return
	}
	activeKey, activeName, ok := p.Current(target)
	for _, src := range p.Sources {
		state := overrideOff
		if ok && activeKey == src.Key && activeName != "" {
			state = activeName
		}
		c.client.Publish(c.overrideTopic(target, src.Key, "state"), 1, true, state)
	}
}

// overrideTopic builds <prefix>[/<target>]/override/<key>/<leaf>.
func (c *Client) overrideTopic(target, key, leaf string) string {
	if target == "" {
		return fmt.Sprintf("%s/override/%s/%s", c.topicPrefix, key, leaf)
	}
	return fmt.Sprintf("%s/%s/override/%s/%s", c.topicPrefix, target, key, leaf)
}

func (c *Client) device(target string) (string, haDevice) {
	id, name := c.clientID, "Immich Kiosk (all)"
	if target != "" {
		id, name = "immich-kiosk-"+target, "Immich Kiosk - "+target
	}
	return id, haDevice{
		Identifiers:  []string{id},
		Name:         name,
		Model:        "Immich Kiosk",
		Manufacturer: "immich-kiosk",
	}
}

// publishNavDiscovery publishes the next, previous and reload buttons for target ("" = all clients).
func (c *Client) publishNavDiscovery(client pahomqtt.Client, target string) {
	commandTopic := c.topicPrefix + "/command"
	if target != "" {
		commandTopic = fmt.Sprintf("%s/%s/command", c.topicPrefix, target)
	}
	deviceID, device := c.device(target)

	buttons := []struct{ name, payload, icon string }{
		{"Image: next", string(CommandNext), ""},
		{"Image: previous", string(CommandPrevious), ""},
		{"Page: reload", string(CommandReload), "mdi:refresh"},
	}

	for _, b := range buttons {
		cfg := haButtonConfig{
			Name:              b.name,
			UniqueID:          deviceID + "-" + b.payload,
			CommandTopic:      commandTopic,
			PayloadPress:      b.payload,
			Icon:              b.icon,
			AvailabilityTopic: c.availTopic,
			Device:            device,
		}
		c.publishJSON(client, fmt.Sprintf("homeassistant/button/%s/%s/config", deviceID, b.payload), cfg)
	}
}

func (c *Client) publishOverrideDiscovery(client pahomqtt.Client) {
	p := c.overrideProvider()
	if p == nil {
		return
	}

	// Remove entities published by earlier versions of this feature.
	for _, src := range p.Sources {
		client.Publish(fmt.Sprintf("homeassistant/button/%s-override-%s-stop/config", c.clientID, src.Key), 1, true, "")
	}

	c.publishOverrideSelects(client, p, "")
	for _, name := range c.overrideTargets(p) {
		c.publishOverrideSelects(client, p, name)
	}
}

// overrideTargets returns the connected clients plus those seen on the broker,
// so a client that is offline does not keep a stale image list.
func (c *Client) overrideTargets(p *OverrideProvider) []string {
	c.seenMu.Lock()
	names := slices.Collect(maps.Keys(c.seenClients))
	c.seenMu.Unlock()
	if p.Clients != nil {
		names = append(names, p.Clients()...)
	}
	names = slices.DeleteFunc(names, func(name string) bool { return !isValidClientName(name) })
	slices.Sort(names)
	return slices.Compact(names)
}

// rememberClient records a client seen on the broker and reports whether it is new.
func (c *Client) rememberClient(name string) bool {
	c.seenMu.Lock()
	defer c.seenMu.Unlock()
	if _, ok := c.seenClients[name]; ok {
		return false
	}
	if c.seenClients == nil {
		c.seenClients = make(map[string]struct{})
	}
	c.seenClients[name] = struct{}{}
	return true
}

// publishOverrideSelects publishes one select per source on target's device
// ("" = the all-clients device), followed by the current state.
func (c *Client) publishOverrideSelects(client pahomqtt.Client, p *OverrideProvider, target string) {
	deviceID, device := c.device(target)

	for _, src := range p.Sources {
		options := append([]string{overrideOff}, p.AssetNames(src.Key)...)

		cfg := haSelectConfig{
			Name:              "Override: " + src.Name,
			UniqueID:          fmt.Sprintf("%s-override-%s-select", deviceID, src.Key),
			CommandTopic:      c.overrideTopic(target, src.Key, "set"),
			StateTopic:        c.overrideTopic(target, src.Key, "state"),
			Options:           options,
			Icon:              "mdi:image-lock",
			AvailabilityTopic: c.availTopic,
			Device:            device,
		}
		c.publishJSON(client, fmt.Sprintf("homeassistant/select/%s-override-%s/config", deviceID, src.Key), cfg)
	}

	c.publishJSON(client, fmt.Sprintf("homeassistant/number/%s-override-duration/config", deviceID), haNumberConfig{
		Name:              "Override: duration",
		UniqueID:          deviceID + "-override-duration",
		CommandTopic:      c.draftTopic(target, fieldOverrideDuration, "set"),
		StateTopic:        c.draftTopic(target, fieldOverrideDuration, "state"),
		Min:               1,
		Max:               c.maxMinutes(fieldOverrideDuration),
		Step:              1,
		Mode:              "box",
		UnitOfMeasurement: "min",
		Icon:              "mdi:timer-outline",
		EntityCategory:    "config",
		AvailabilityTopic: c.availTopic,
		Device:            device,
	})
	c.scheduleDraftDefaults(target, fieldOverrideDuration)

	c.PublishOverrideState(target)
}

func (c *Client) publishJSON(client pahomqtt.Client, topic string, v any) {
	payload, err := json.Marshal(v)
	if err != nil {
		log.Error("MQTT discovery marshal failed", "topic", topic, "err", err)
		return
	}
	client.Publish(topic, 1, true, payload)
}

// topicParts splits a topic below the prefix into its segments.
func (c *Client) topicParts(topic string) []string {
	return strings.Split(strings.TrimPrefix(topic, c.topicPrefix+"/"), "/")
}

// messageHandler processes navigation commands.
func (c *Client) messageHandler(_ pahomqtt.Client, msg pahomqtt.Message) {
	payload := strings.TrimSpace(strings.ToLower(string(msg.Payload())))
	parts := c.topicParts(msg.Topic())

	// prefix/command → all clients; prefix/<client>/command → one client.
	var target string
	switch len(parts) {
	case 1:
	case 2:
		target = parts[0]
		if target == "override" || target == "message" {
			// prefix/+/command also matches prefix/override/command and prefix/message/command.
			return
		}
	default:
		return
	}

	cmd := Command(payload)
	if cmd != CommandNext && cmd != CommandPrevious && cmd != CommandReload {
		log.Warn("MQTT unknown navigation command", "payload", payload)
		return
	}

	c.handlersMu.RLock()
	handlers := c.handlers
	c.handlersMu.RUnlock()
	for _, h := range handlers {
		h(cmd, target)
	}
}

// overrideCommandHandler processes …/override/command messages:
//
//	start <source> [<description or filename>]
//	stop
//	refresh-sources [<source>]
func (c *Client) overrideCommandHandler(_ pahomqtt.Client, msg pahomqtt.Message) {
	parts := c.topicParts(msg.Topic())

	var target string
	switch {
	case len(parts) == 2 && parts[0] == "override":
	case len(parts) == 3 && parts[1] == "override":
		target = parts[0]
	default:
		return
	}

	fields := strings.Fields(string(msg.Payload()))
	if len(fields) == 0 {
		return
	}

	cmd := OverrideCommand{Action: OverrideAction(strings.ToLower(fields[0])), Target: target}
	switch cmd.Action {
	case OverrideStop:
	case OverrideRefresh:
		if len(fields) > 1 {
			cmd.SourceKey = fields[1]
		}
	case OverrideStart:
		if len(fields) < 2 {
			log.Warn("MQTT override: start needs a source key", "payload", string(msg.Payload()))
			return
		}
		cmd.SourceKey = fields[1]
		// Descriptions and filenames may contain spaces.
		cmd.AssetName = strings.Join(fields[2:], " ")
		cmd.Duration = c.draftDuration(target, fieldOverrideDuration)
	default:
		log.Warn("MQTT override: unknown command", "payload", string(msg.Payload()))
		return
	}

	c.dispatchOverride(cmd)
}

// overrideSelectHandler processes HA select input on …/override/<source>/set.
func (c *Client) overrideSelectHandler(_ pahomqtt.Client, msg pahomqtt.Message) {
	parts := c.topicParts(msg.Topic())

	var target, key string
	switch {
	case len(parts) == 3 && parts[0] == "override":
		key = parts[1]
	case len(parts) == 4 && parts[1] == "override":
		target, key = parts[0], parts[2]
	default:
		return
	}

	value := strings.TrimSpace(string(msg.Payload()))
	if key == overrideDurationKey {
		c.handleDraftSet(target, fieldOverrideDuration, value)
		return
	}

	cmd := OverrideCommand{
		Action:    OverrideStart,
		SourceKey: key,
		AssetName: value,
		Target:    target,
		Duration:  c.draftDuration(target, fieldOverrideDuration),
	}
	if strings.EqualFold(value, overrideOff) {
		cmd = OverrideCommand{Action: OverrideStop, SourceKey: key, Target: target}
	}

	c.dispatchOverride(cmd)
}

// overrideStateHandler reads retained override states: it restores the
// override duration and remembers clients that are not connected.
func (c *Client) overrideStateHandler(client pahomqtt.Client, msg pahomqtt.Message) {
	parts := c.topicParts(msg.Topic())

	var target, key string
	switch {
	case len(parts) == 3 && parts[0] == "override":
		key = parts[1]
	case len(parts) == 4 && parts[1] == "override" && isValidClientName(parts[0]):
		target, key = parts[0], parts[2]
	default:
		return
	}

	if key == overrideDurationKey {
		c.setDraft(target, fieldOverrideDuration, string(msg.Payload()))
	}

	// The list may have been published before this state arrived; update the
	// client now instead of waiting for the next change.
	if target != "" && c.rememberClient(target) {
		if p := c.overrideProvider(); p != nil && client != nil {
			go c.publishOverrideSelects(client, p, target)
		}
	}
}

func (c *Client) dispatchOverride(cmd OverrideCommand) {
	p := c.overrideProvider()
	if p == nil {
		return
	}
	log.Debug("MQTT override command", "action", cmd.Action, "source", cmd.SourceKey, "asset", cmd.AssetName, "target", cmd.Target)
	// Run off the paho callback goroutine: handling may call the Immich API.
	go p.Handle(cmd)
}
