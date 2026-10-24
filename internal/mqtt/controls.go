package mqtt

import (
	"fmt"
	"strings"
	"time"

	"charm.land/log/v2"
	pahomqtt "github.com/eclipse/paho.mqtt.golang"
)

// Switch payloads, Home Assistant's defaults.
const (
	switchOn  = "ON"
	switchOff = "OFF"
)

// SelectControl is a Home Assistant select on every device, such as
// "Album: show". Its topics are <prefix>[/<client>]/<Field>/set and …/state.
type SelectControl struct {
	Field string
	Name  string
	Icon  string
	// Options returns the select options of target. known is false while
	// target's options are not known yet; no options means no select.
	Options func(target string) (options []string, known bool)
	// Current returns the select state of target.
	Current func(target string) string
	// Select handles a selection from Home Assistant and reports whether the
	// option was valid.
	Select func(target, option string) bool
	// Restore applies a retained state after a restart and reports whether
	// it was applied.
	Restore func(target, option string) bool
}

// SwitchControl is a Home Assistant switch on every device, such as
// "Slideshow: pause". Its topics are <prefix>[/<client>]/<Field>/set and …/state.
type SwitchControl struct {
	Field string
	Name  string
	Icon  string
	// Get returns the switch state of exactly target.
	Get func(target string) bool
	// Set handles a change from Home Assistant.
	Set func(target string, on bool)
	// Restore applies a retained state after a restart and reports whether
	// it was applied.
	Restore func(target string, on bool) bool
}

// controls are the selects and switches enabled with EnableControls.
type controls struct {
	selects  []SelectControl
	switches []SwitchControl
}

type haSwitchConfig struct {
	Name              string   `json:"name"`
	UniqueID          string   `json:"unique_id"`
	CommandTopic      string   `json:"command_topic"`
	StateTopic        string   `json:"state_topic"`
	PayloadOn         string   `json:"payload_on"`
	PayloadOff        string   `json:"payload_off"`
	Icon              string   `json:"icon,omitempty"`
	AvailabilityTopic string   `json:"availability_topic"`
	Device            haDevice `json:"device"`
}

// EnableControls connects the selects and switches, subscribes to their
// topics and publishes them on the all-clients device.
func (c *Client) EnableControls(selects []SelectControl, switches []SwitchControl) {
	c.controlsMu.Lock()
	c.controls = &controls{selects: selects, switches: switches}
	c.controlsMu.Unlock()

	c.subscribeControls(c.client)
	c.PublishControlDiscovery("")
}

func (c *Client) enabledControls() *controls {
	c.controlsMu.RLock()
	defer c.controlsMu.RUnlock()
	return c.controls
}

func (c *Client) subscribeControls(client pahomqtt.Client) {
	ctl := c.enabledControls()
	if ctl == nil {
		return
	}

	p := c.topicPrefix
	subscribe := func(field string, onSet, onState func(target, value string)) {
		c.subscribe(client, p+"/"+field+"/set", c.controlHandler(field, "set", onSet))
		c.subscribe(client, p+"/+/"+field+"/set", c.controlHandler(field, "set", onSet))
		// Retained states restore the controls after a restart.
		c.subscribe(client, p+"/"+field+"/state", c.controlHandler(field, "state", onState))
		c.subscribe(client, p+"/+/"+field+"/state", c.controlHandler(field, "state", onState))
	}

	for _, sel := range ctl.selects {
		subscribe(sel.Field,
			func(target, value string) { c.handleSelectSet(sel, target, value) },
			func(target, value string) { c.handleSelectState(sel, target, value) })
	}
	for _, sw := range ctl.switches {
		subscribe(sw.Field,
			func(target, value string) { c.handleSwitchSet(sw, target, value) },
			func(target, value string) { c.handleSwitchState(sw, target, value) })
	}
}

// controlHandler calls handle for messages on <prefix>[/<client>]/<field>/<leaf>.
func (c *Client) controlHandler(field, leaf string, handle func(target, value string)) pahomqtt.MessageHandler {
	return func(_ pahomqtt.Client, msg pahomqtt.Message) {
		target, ok := c.controlTarget(msg.Topic(), field, leaf)
		if !ok {
			return
		}
		handle(target, strings.TrimSpace(string(msg.Payload())))
	}
}

// controlTarget returns the target of a <prefix>[/<client>]/<field>/<leaf> topic.
func (c *Client) controlTarget(topic, field, leaf string) (string, bool) {
	parts := c.topicParts(topic)
	switch {
	case len(parts) == 2 && parts[0] == field && parts[1] == leaf:
		return "", true
	// prefix/+/<field>/… also matches the topics of an override source named <field>.
	case len(parts) == 3 && parts[1] == field && parts[2] == leaf && isValidClientName(parts[0]):
		return parts[0], true
	}
	return "", false
}

func (c *Client) handleSelectSet(sel SelectControl, target, option string) {
	log.Debug("MQTT select", "field", sel.Field, "target", target, "option", option)
	// Run off the paho callback goroutine: handling may publish.
	go func() {
		if !sel.Select(target, option) {
			log.Warn("MQTT select: unknown option", "field", sel.Field, "target", target, "option", option)
		}
		c.PublishSelectState(sel.Field, target)
	}()
}

func (c *Client) handleSelectState(sel SelectControl, target, option string) {
	if option == "" || !sel.Restore(target, option) {
		return
	}
	// The select may have been published before this state arrived. Our own
	// state comes back here too, but is not applied twice.
	if _, known := sel.Options(target); known {
		go c.PublishSelectState(sel.Field, target)
	}
}

func (c *Client) handleSwitchSet(sw SwitchControl, target, value string) {
	on, ok := parseSwitch(value)
	if !ok {
		log.Warn("MQTT switch: invalid value", "field", sw.Field, "target", target, "value", value)
		return
	}
	log.Debug("MQTT switch", "field", sw.Field, "target", target, "on", on)
	go func() {
		sw.Set(target, on)
		c.PublishSwitchState(sw.Field, target)
	}()
}

func (c *Client) handleSwitchState(sw SwitchControl, target, value string) {
	on, ok := parseSwitch(value)
	if !ok || !sw.Restore(target, on) {
		return
	}
	// The default state may have been published before this state arrived.
	go c.PublishSwitchState(sw.Field, target)
}

func parseSwitch(value string) (on, ok bool) {
	switch strings.ToUpper(value) {
	case switchOn:
		return true, true
	case switchOff:
		return false, true
	}
	return false, false
}

// PublishControlDiscovery publishes the selects and switches of target's
// device ("" = all clients). A select is removed when target has too few
// options, and left alone while its options are not known yet.
func (c *Client) PublishControlDiscovery(target string) {
	ctl := c.enabledControls()
	if ctl == nil {
		return
	}
	for _, sel := range ctl.selects {
		c.PublishSelectDiscovery(sel.Field, target)
	}

	deviceID, device := c.device(target)
	for _, sw := range ctl.switches {
		c.publishJSON(c.client, fmt.Sprintf("homeassistant/switch/%s-%s/config", deviceID, sw.Field), haSwitchConfig{
			Name:              sw.Name,
			UniqueID:          fmt.Sprintf("%s-%s-switch", deviceID, sw.Field),
			CommandTopic:      c.draftTopic(target, sw.Field, "set"),
			StateTopic:        c.draftTopic(target, sw.Field, "state"),
			PayloadOn:         switchOn,
			PayloadOff:        switchOff,
			Icon:              sw.Icon,
			AvailabilityTopic: c.availTopic,
			Device:            device,
		})
		c.publishStateLater(func() { c.PublishSwitchState(sw.Field, target) })
	}
}

// publishStateLater publishes a state once retained states have had time to
// arrive. Publishing the default at once could overwrite the retained state
// before it is restored, e.g. when the connection drops right after startup.
func (c *Client) publishStateLater(publish func()) {
	time.AfterFunc(draftDefaultsDelay, publish)
}

// PublishSelectDiscovery publishes one select of target's device.
func (c *Client) PublishSelectDiscovery(field, target string) {
	sel, ok := c.selectControl(field)
	if !ok {
		return
	}
	options, known := sel.Options(target)
	if !known {
		return
	}

	deviceID, device := c.device(target)
	topic := fmt.Sprintf("homeassistant/select/%s-%s/config", deviceID, field)

	if len(options) == 0 {
		c.client.Publish(topic, 1, true, "")
		return
	}

	c.publishJSON(c.client, topic, haSelectConfig{
		Name:              sel.Name,
		UniqueID:          fmt.Sprintf("%s-%s-select", deviceID, field),
		CommandTopic:      c.draftTopic(target, field, "set"),
		StateTopic:        c.draftTopic(target, field, "state"),
		Options:           options,
		Icon:              sel.Icon,
		AvailabilityTopic: c.availTopic,
		Device:            device,
	})
	c.publishStateLater(func() { c.PublishSelectState(field, target) })
}

// PublishSelectState publishes the state of one select of target.
func (c *Client) PublishSelectState(field, target string) {
	if sel, ok := c.selectControl(field); ok {
		c.client.Publish(c.draftTopic(target, field, "state"), 1, true, sel.Current(target))
	}
}

// PublishSwitchState publishes the state of one switch of target.
func (c *Client) PublishSwitchState(field, target string) {
	ctl := c.enabledControls()
	if ctl == nil {
		return
	}
	for _, sw := range ctl.switches {
		if sw.Field == field {
			state := switchOff
			if sw.Get(target) {
				state = switchOn
			}
			c.client.Publish(c.draftTopic(target, field, "state"), 1, true, state)
		}
	}
}

func (c *Client) selectControl(field string) (SelectControl, bool) {
	ctl := c.enabledControls()
	if ctl == nil {
		return SelectControl{}, false
	}
	for _, sel := range ctl.selects {
		if sel.Field == field {
			return sel, true
		}
	}
	return SelectControl{}, false
}
