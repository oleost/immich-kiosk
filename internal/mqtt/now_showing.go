package mqtt

import "fmt"

// nowShowingField is the topic path below <prefix>[/<client>] of what a
// screen shows: …/image/state, …/image/attributes and …/image/preview.
const nowShowingField = "image"

type haSensorConfig struct {
	Name                string   `json:"name"`
	UniqueID            string   `json:"unique_id"`
	StateTopic          string   `json:"state_topic"`
	JSONAttributesTopic string   `json:"json_attributes_topic"`
	Icon                string   `json:"icon,omitempty"`
	AvailabilityTopic   string   `json:"availability_topic"`
	Device              haDevice `json:"device"`
}

type haImageConfig struct {
	Name              string   `json:"name"`
	UniqueID          string   `json:"unique_id"`
	ImageTopic        string   `json:"image_topic"`
	ContentType       string   `json:"content_type"`
	AvailabilityTopic string   `json:"availability_topic"`
	Device            haDevice `json:"device"`
}

// publishNowShowingDiscovery publishes the "Image: current" sensor and the
// "Image: preview" image of target's device ("" = all clients).
func (c *Client) publishNowShowingDiscovery(target string) {
	deviceID, device := c.device(target)

	c.publishJSON(c.client, fmt.Sprintf("homeassistant/sensor/%s-image/config", deviceID), haSensorConfig{
		Name:                "Image: current",
		UniqueID:            deviceID + "-image-current",
		StateTopic:          c.draftTopic(target, nowShowingField, "state"),
		JSONAttributesTopic: c.draftTopic(target, nowShowingField, "attributes"),
		Icon:                "mdi:image-text",
		AvailabilityTopic:   c.availTopic,
		Device:              device,
	})

	c.publishJSON(c.client, fmt.Sprintf("homeassistant/image/%s-image/config", deviceID), haImageConfig{
		Name:              "Image: preview",
		UniqueID:          deviceID + "-image-preview",
		ImageTopic:        c.draftTopic(target, nowShowingField, "preview"),
		ContentType:       "image/jpeg",
		AvailabilityTopic: c.availTopic,
		Device:            device,
	})
}

// PublishNowShowing publishes what target ("" = any screen) shows: a short
// state, its attributes as JSON, and a JPEG preview (skipped when empty).
// Everything is retained so Home Assistant shows it after a restart.
func (c *Client) PublishNowShowing(target, state string, attributes, preview []byte) {
	c.client.Publish(c.draftTopic(target, nowShowingField, "state"), 1, true, state)
	c.client.Publish(c.draftTopic(target, nowShowingField, "attributes"), 1, true, attributes)
	if len(preview) > 0 {
		c.client.Publish(c.draftTopic(target, nowShowingField, "preview"), 1, true, preview)
	}
}
