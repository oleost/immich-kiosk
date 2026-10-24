package routes

import (
	"context"
	"encoding/json"
	"slices"

	"charm.land/log/v2"

	"github.com/damongolding/immich-kiosk/internal/config"
	"github.com/damongolding/immich-kiosk/internal/focus"
	"github.com/damongolding/immich-kiosk/internal/immich"
	"github.com/damongolding/immich-kiosk/internal/mqtt"
	"github.com/damongolding/immich-kiosk/internal/switches"
)

// Fields of the Home Assistant controls; also their MQTT topic segments.
const (
	fieldAlbum     = "album"
	fieldPerson    = "person"
	fieldPause     = "pause"
	fieldSleep     = "sleep"
	fieldFavorites = "favorites"
	fieldMemories  = "memories"
)

// sseEventControl tells the browser whether it is paused or asleep.
const sseEventControl = "kiosk-control"

// The control stores; nil when MQTT is not connected.
var (
	albumFocusStore  *focus.Store
	personFocusStore *focus.Store
	switchStore      *switches.Store
	controlsMQTT     *mqtt.Client
)

// controlPayload is the data of a kiosk-control SSE event.
type controlPayload struct {
	Pause bool `json:"pause"`
	Sleep bool `json:"sleep"`
}

// SetupControls creates the control stores and exposes them to Home Assistant
// as selects ("Album: show", "Person: show") and switches.
func SetupControls(ctx context.Context, baseConfig *config.Config, mqttClient *mqtt.Client) {
	albums := focus.New(focus.AlbumKeywords, albumNameResolver(ctx, baseConfig))
	people := focus.New(focus.PersonKeywords, personNameResolver(ctx, baseConfig))
	sw := switches.New()

	albums.SetOnChange(func(target string) { mqttClient.PublishSelectDiscovery(fieldAlbum, target) })
	people.SetOnChange(func(target string) { mqttClient.PublishSelectDiscovery(fieldPerson, target) })

	albumFocusStore, personFocusStore, switchStore, controlsMQTT = albums, people, sw, mqttClient

	selects := []mqtt.SelectControl{
		focusSelect(fieldAlbum, "Album: show", "mdi:image-album", albums, fieldPerson, people, mqttClient.PublishSelectState),
		focusSelect(fieldPerson, "Person: show", "mdi:account-heart", people, fieldAlbum, albums, mqttClient.PublishSelectState),
	}

	switchControls := []mqtt.SwitchControl{
		switchControl(sw, fieldPause, "Slideshow: pause", "mdi:pause", pushControl, pushControl),
		switchControl(sw, fieldSleep, "Slideshow: sleep", "mdi:sleep", pushControl, pushControl),
		// A restored filter applies from the next asset; "next" would be queued.
		switchControl(sw, fieldFavorites, "Filter: favorites", "mdi:heart", nextAsset, nil),
		switchControl(sw, fieldMemories, "Filter: memories", "mdi:calendar-heart", nextAsset, nil),
	}

	mqttClient.EnableControls(selects, switchControls)

	albums.Learn("", focusAlbums(baseConfig, baseConfig.Albums))
	people.Learn("", baseConfig.People)
}

// focusSelect builds a select for store. Album and person focus exclude each
// other on the same device: picking one resets the other to Default, and its
// new state is published with publishState.
func focusSelect(field, name, icon string, store *focus.Store, otherField string, other *focus.Store, publishState func(field, target string)) mqtt.SelectControl {
	return mqtt.SelectControl{
		Field:   field,
		Name:    name,
		Icon:    icon,
		Options: store.Options,
		Current: store.Current,
		Select: func(target, option string) bool {
			if !store.Select(target, option) {
				return false
			}
			if _, ok := store.Selected(target); ok {
				if _, otherSet := other.Selected(target); otherSet {
					other.Select(target, focus.DefaultOption)
					publishState(otherField, target)
				}
			}
			nextAsset(target)
			return true
		},
		Restore: store.Restore,
	}
}

// switchControl builds a switch stored in sw. changed is called with the
// target after it was switched in Home Assistant, restored (if not nil) after
// a retained state was applied.
func switchControl(sw *switches.Store, field, name, icon string, changed, restored func(target string)) mqtt.SwitchControl {
	return mqtt.SwitchControl{
		Field: field,
		Name:  name,
		Icon:  icon,
		Get:   func(target string) bool { return sw.Get(target, field) },
		Set: func(target string, on bool) {
			sw.Set(target, field, on)
			changed(target)
		},
		Restore: func(target string, on bool) bool {
			if !sw.Restore(target, field, on) {
				return false
			}
			if restored != nil {
				restored(target)
			}
			return true
		},
	}
}

// nextAsset tells the clients of target ("" = all clients) to show a new asset.
func nextAsset(target string) {
	sseHub.broadcast(target, string(mqtt.CommandNext))
}

// pushControl sends every client of target ("" = all clients) whether it is
// paused or asleep.
func pushControl(target string) {
	sseHub.sendEach(target, func(clientName string) sseEvent {
		return controlEvent(clientName)
	})
}

// controlEvent builds the kiosk-control event for an SSE client name.
func controlEvent(clientName string) sseEvent {
	var p controlPayload
	if switchStore != nil {
		name := controlClient(clientName)
		p = controlPayload{
			Pause: switchStore.Active(name, fieldPause),
			Sleep: switchStore.Active(name, fieldSleep),
		}
	}
	data, err := json.Marshal(p)
	if err != nil {
		log.Error("controls: marshal event", "err", err)
	}
	return sseEvent{name: sseEventControl, data: string(data)}
}

// controlClient maps a client name to a control target: "" when the name is
// missing or invalid, so the client only follows the all-clients controls.
func controlClient(clientName string) string {
	if name := validSSEClientName(clientName); name != "_global" {
		return name
	}
	return ""
}

// sleepForced reports whether sleep was switched on for clientName in Home
// Assistant, regardless of the sleep schedule.
func sleepForced(clientName string) bool {
	return switchStore != nil && switchStore.Active(controlClient(clientName), fieldSleep)
}

// albumNameResolver looks up Immich album names, using the API key of the
// user named in an "id@user" entry.
func albumNameResolver(ctx context.Context, baseConfig *config.Config) focus.NameResolver {
	return nameResolver(ctx, baseConfig, func(asset *immich.Asset) (map[string]string, error) {
		albums, err := asset.AllAlbums("controls", "")
		names := make(map[string]string, len(albums))
		for _, a := range albums {
			names[a.ID] = a.AlbumName
		}
		return names, err
	})
}

// personNameResolver looks up Immich person names, like albumNameResolver.
func personNameResolver(ctx context.Context, baseConfig *config.Config) focus.NameResolver {
	return nameResolver(ctx, baseConfig, func(asset *immich.Asset) (map[string]string, error) {
		people, err := asset.AllNamedPeople("controls", "")
		names := make(map[string]string, len(people))
		for _, p := range people {
			names[p.ID] = p.Name
		}
		return names, err
	})
}

// nameResolver resolves entries with the names listed by list, which is
// called once per Immich user.
func nameResolver(ctx context.Context, baseConfig *config.Config, list func(asset *immich.Asset) (map[string]string, error)) focus.NameResolver {
	return func(entries []string) []string {
		names := make([]string, len(entries))
		byUser := map[string]map[string]string{}

		for i, entry := range entries {
			asset := immich.New(ctx, *baseConfig)
			id, user := asset.ApplyUserFromAssetID(entry)

			userNames, ok := byUser[user]
			if !ok {
				var err error
				userNames, err = list(&asset)
				if err != nil {
					log.Error("controls: getting names", "user", user, "err", err)
				}
				byUser[user] = userNames
			}
			names[i] = userNames[id]
		}
		return names
	}
}

// focusAlbums returns the albums a target offers: its own albums plus the
// on-demand albums, which are never shown by default.
func focusAlbums(baseConfig *config.Config, albums []string) []string {
	return slices.Concat(albums, baseConfig.AlbumsOnDemand)
}

// learnSources records the albums and people of the all-clients target and
// of clientName, before any override or focus changes them.
func learnSources(baseConfig *config.Config, requestConfig *config.Config, clientName string) {
	if albumFocusStore == nil {
		return
	}
	// The config can be reloaded while Kiosk is running.
	albumFocusStore.Learn("", focusAlbums(baseConfig, baseConfig.Albums))
	personFocusStore.Learn("", baseConfig.People)
	if name := controlClient(clientName); name != "" {
		albumFocusStore.Learn(name, focusAlbums(baseConfig, requestConfig.Albums))
		personFocusStore.Learn(name, requestConfig.People)
	}
}

// applyControls applies the Home Assistant controls to requestConfig: the
// album or person focus, and the favorites and memories filters. It reports
// whether anything was changed.
func applyControls(ctx context.Context, requestConfig *config.Config, clientName, requestID, deviceID string) bool {
	if albumFocusStore == nil {
		return false
	}
	name := controlClient(clientName)
	changed := applyFocus(requestConfig, name)

	if switchStore.Active(name, fieldMemories) && hasMemories(ctx, *requestConfig, requestID, deviceID) {
		requestConfig.ResetBuckets()
		requestConfig.Memories = true
		requestConfig.MemoriesOnly = true
		changed = true
	}

	if switchStore.Active(name, fieldFavorites) {
		requestConfig.FilterFavorites = true
		changed = true
	}
	return changed
}

// applyFocus narrows the asset sources down to the focused album or person.
// A focus set for the client wins over the all-clients focus; on one device
// only one of the two can be set.
func applyFocus(requestConfig *config.Config, clientName string) bool {
	targets := []string{""}
	if clientName != "" {
		targets = []string{clientName, ""}
	}

	for _, target := range targets {
		if album, ok := albumFocusStore.Selected(target); ok {
			narrowSources(requestConfig)
			requestConfig.Albums = []string{album}
			return true
		}
		if person, ok := personFocusStore.Selected(target); ok {
			narrowSources(requestConfig)
			requestConfig.People = []string{person}
			return true
		}
	}
	return false
}

// narrowSources clears every asset source; filters such as filter_date stay.
func narrowSources(requestConfig *config.Config) {
	requestConfig.ResetBuckets()
	requestConfig.Memories = false
	requestConfig.MemoriesOnly = false
}

// hasMemories reports whether Immich has memories for today, so the memories
// filter can fall back to the normal slideshow when there are none.
func hasMemories(ctx context.Context, requestConfig config.Config, requestID, deviceID string) bool {
	asset := immich.New(ctx, requestConfig)
	return asset.MemoriesAssetsCount(requestID, deviceID) > 0
}
