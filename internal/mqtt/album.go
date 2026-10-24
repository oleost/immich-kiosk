package mqtt

import (
	"fmt"
	"strings"

	"charm.land/log/v2"
	pahomqtt "github.com/eclipse/paho.mqtt.golang"
)

// albumField is the topic path of the album focus select below <prefix>[/<client>].
const albumField = "album"

// AlbumProvider connects the MQTT layer to the album focus store.
type AlbumProvider struct {
	// Options returns the select options of target. known is false while
	// target's albums are not known yet; no options means no select.
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

// EnableAlbumFocus connects the album focus store, subscribes to its topics
// and publishes the all-clients select.
func (c *Client) EnableAlbumFocus(p AlbumProvider) {
	c.albumMu.Lock()
	c.album = &p
	c.albumMu.Unlock()

	c.subscribeAlbum(c.client)
	c.PublishAlbumDiscovery("")
}

func (c *Client) albumProvider() *AlbumProvider {
	c.albumMu.RLock()
	defer c.albumMu.RUnlock()
	return c.album
}

func (c *Client) subscribeAlbum(client pahomqtt.Client) {
	p := c.topicPrefix
	c.subscribe(client, p+"/"+albumField+"/set", c.albumSetHandler)
	c.subscribe(client, p+"/+/"+albumField+"/set", c.albumSetHandler)
	// Retained states restore the selection after a restart.
	c.subscribe(client, p+"/"+albumField+"/state", c.albumStateHandler)
	c.subscribe(client, p+"/+/"+albumField+"/state", c.albumStateHandler)
}

// PublishAlbumDiscovery publishes the album select for target ("" = all
// clients), or removes it when target has too few albums. Nothing is
// published while target's albums are not known yet.
func (c *Client) PublishAlbumDiscovery(target string) {
	p := c.albumProvider()
	if p == nil {
		return
	}
	options, known := p.Options(target)
	if !known {
		return
	}

	deviceID, device := c.device(target)
	topic := fmt.Sprintf("homeassistant/select/%s-album/config", deviceID)

	if len(options) == 0 {
		c.client.Publish(topic, 1, true, "")
		return
	}

	c.publishJSON(c.client, topic, haSelectConfig{
		Name:              "Album: show",
		UniqueID:          deviceID + "-album-select",
		CommandTopic:      c.draftTopic(target, albumField, "set"),
		StateTopic:        c.draftTopic(target, albumField, "state"),
		Options:           options,
		Icon:              "mdi:image-album",
		AvailabilityTopic: c.availTopic,
		Device:            device,
	})
	c.PublishAlbumState(target)
}

// PublishAlbumState publishes the album select state of target.
func (c *Client) PublishAlbumState(target string) {
	if p := c.albumProvider(); p != nil {
		c.client.Publish(c.draftTopic(target, albumField, "state"), 1, true, p.Current(target))
	}
}

// albumTarget returns the target of …/album/<leaf> topics.
func (c *Client) albumTarget(topic, leaf string) (string, bool) {
	parts := c.topicParts(topic)
	switch {
	case len(parts) == 2 && parts[0] == albumField && parts[1] == leaf:
		return "", true
	// prefix/+/album/… also matches the topics of an override source named "album".
	case len(parts) == 3 && parts[1] == albumField && parts[2] == leaf && isValidClientName(parts[0]):
		return parts[0], true
	}
	return "", false
}

// albumSetHandler processes HA select input on …/album/set.
func (c *Client) albumSetHandler(_ pahomqtt.Client, msg pahomqtt.Message) {
	target, ok := c.albumTarget(msg.Topic(), "set")
	p := c.albumProvider()
	if !ok || p == nil {
		return
	}

	option := strings.TrimSpace(string(msg.Payload()))
	log.Debug("MQTT album focus", "target", target, "album", option)

	go func() {
		if !p.Select(target, option) {
			log.Warn("MQTT album focus: unknown album", "target", target, "album", option)
		}
		c.PublishAlbumState(target)
	}()
}

// albumStateHandler restores the selection from retained states.
func (c *Client) albumStateHandler(_ pahomqtt.Client, msg pahomqtt.Message) {
	target, ok := c.albumTarget(msg.Topic(), "state")
	p := c.albumProvider()
	if !ok || p == nil {
		return
	}
	option := strings.TrimSpace(string(msg.Payload()))
	if option == "" || !p.Restore(target, option) {
		return
	}
	// The select may have been published before this state arrived. Our own
	// state comes back here too, but is not applied twice.
	if _, known := p.Options(target); known {
		c.PublishAlbumState(target)
	}
}
