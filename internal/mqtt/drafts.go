package mqtt

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/log/v2"
)

// Fields set in Home Assistant and kept as retained states, so they survive a
// restart. The field is also the topic path below <prefix>[/<client>].
const (
	fieldMessageText      = "message/text"
	fieldMessageDuration  = "message/duration"
	fieldOverrideDuration = "override/duration"
)

// draftDefaultsDelay gives retained states time to arrive before defaults are
// published for fields that have none.
const draftDefaultsDelay = 3 * time.Second

// draftKey identifies one field for one target ("" = all clients).
type draftKey struct {
	target string
	field  string
}

// draftTopic builds <prefix>[/<target>]/<field>/<leaf>.
func (c *Client) draftTopic(target, field, leaf string) string {
	if target == "" {
		return fmt.Sprintf("%s/%s/%s", c.topicPrefix, field, leaf)
	}
	return fmt.Sprintf("%s/%s/%s/%s", c.topicPrefix, target, field, leaf)
}

// setDraft validates and stores a field value. It returns the stored value and
// whether it was valid.
func (c *Client) setDraft(target, field, value string) (string, bool) {
	switch field {
	case fieldMessageText:
	case fieldMessageDuration, fieldOverrideDuration:
		m, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil || m < 1 {
			log.Warn("MQTT: invalid duration", "field", field, "value", value)
			return "", false
		}
		value = strconv.FormatFloat(min(m, c.maxMinutes(field)), 'f', -1, 64)
	default:
		return "", false
	}

	c.draftsMu.Lock()
	if c.drafts == nil {
		c.drafts = make(map[draftKey]string)
	}
	c.drafts[draftKey{target, field}] = value
	c.draftsMu.Unlock()
	return value, true
}

func (c *Client) draft(target, field string) (string, bool) {
	c.draftsMu.RLock()
	defer c.draftsMu.RUnlock()
	v, ok := c.drafts[draftKey{target, field}]
	return v, ok
}

// draftDuration returns the duration set for field, or the field's default.
func (c *Client) draftDuration(target, field string) time.Duration {
	m := c.defaultMinutes(field)
	if v, ok := c.draft(target, field); ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			m = f
		}
	}
	return time.Duration(m * float64(time.Minute))
}

func (c *Client) maxMinutes(field string) float64 {
	if field == fieldOverrideDuration {
		if p := c.overrideProvider(); p != nil {
			return max(1, p.MaxDuration.Minutes())
		}
	}
	return messageMaxMinutes
}

func (c *Client) defaultMinutes(field string) float64 {
	if field == fieldOverrideDuration {
		return c.maxMinutes(field)
	}
	return messageDefaultMinutes
}

func (c *Client) defaultDraft(field string) string {
	if field == fieldMessageText {
		return ""
	}
	return strconv.FormatFloat(c.defaultMinutes(field), 'f', -1, 64)
}

// handleDraftSet stores a value from Home Assistant and echoes it as state.
func (c *Client) handleDraftSet(target, field, value string) {
	if v, ok := c.setDraft(target, field, value); ok {
		c.client.Publish(c.draftTopic(target, field, "state"), 1, true, v)
	}
}

// scheduleDraftDefaults publishes the default value of each field that still
// has no value once retained states have had time to arrive. Without a state
// Home Assistant shows the entity as "unknown".
func (c *Client) scheduleDraftDefaults(target string, fields ...string) {
	time.AfterFunc(draftDefaultsDelay, func() {
		for _, field := range fields {
			if _, ok := c.draft(target, field); ok {
				continue
			}
			if v, ok := c.setDraft(target, field, c.defaultDraft(field)); ok {
				c.client.Publish(c.draftTopic(target, field, "state"), 1, true, v)
			}
		}
	})
}

// republishDrafts publishes every stored value again, e.g. after Home Assistant
// restarted. An empty text is not retained by the broker, so it must be resent.
func (c *Client) republishDrafts() {
	c.draftsMu.RLock()
	defer c.draftsMu.RUnlock()
	for k, v := range c.drafts {
		c.client.Publish(c.draftTopic(k.target, k.field, "state"), 1, true, v)
	}
}
