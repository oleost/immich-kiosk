package routes

import (
	"context"
	"slices"

	"charm.land/log/v2"

	"github.com/damongolding/immich-kiosk/internal/albumfocus"
	"github.com/damongolding/immich-kiosk/internal/config"
	"github.com/damongolding/immich-kiosk/internal/immich"
	"github.com/damongolding/immich-kiosk/internal/mqtt"
)

// albumFocusStore holds the album focus; nil when MQTT is not connected.
var albumFocusStore *albumfocus.Store

// SetupAlbumFocus creates the album focus store and exposes it to Home Assistant.
func SetupAlbumFocus(ctx context.Context, baseConfig *config.Config, mqttClient *mqtt.Client) {
	store := albumfocus.New(albumNameResolver(ctx, baseConfig))
	store.SetOnChange(mqttClient.PublishAlbumDiscovery)
	albumFocusStore = store

	mqttClient.EnableAlbumFocus(mqtt.AlbumProvider{
		Options: store.Options,
		Current: store.Current,
		Select: func(target, option string) bool {
			if !store.Select(target, option) {
				return false
			}
			sseHub.broadcast(target, string(mqtt.CommandNext))
			return true
		},
		Restore: store.Restore,
	})

	store.Learn("", focusAlbums(baseConfig, baseConfig.Albums))
}

// focusAlbums returns the albums a target offers: its own albums plus the
// on-demand albums, which are never shown by default.
func focusAlbums(baseConfig *config.Config, albums []string) []string {
	return slices.Concat(albums, baseConfig.AlbumsOnDemand)
}

// albumNameResolver looks up Immich album names, using the API key of the
// user named in an "id@user" entry.
func albumNameResolver(ctx context.Context, baseConfig *config.Config) albumfocus.NameResolver {
	return func(entries []string) []string {
		names := make([]string, len(entries))
		byUser := map[string]map[string]string{}

		for i, entry := range entries {
			asset := immich.New(ctx, *baseConfig)
			id, user := asset.ApplyUserFromAssetID(entry)

			albumNames, ok := byUser[user]
			if !ok {
				albumNames = map[string]string{}
				albums, err := asset.AllAlbums("album-focus", "")
				if err != nil {
					log.Error("album focus: getting album names", "user", user, "err", err)
				}
				for _, a := range albums {
					albumNames[a.ID] = a.AlbumName
				}
				byUser[user] = albumNames
			}
			names[i] = albumNames[id]
		}
		return names
	}
}

// learnAlbums records the albums of the all-clients target and of
// clientName, before any override or focus changes them.
func learnAlbums(baseConfig *config.Config, requestConfig *config.Config, clientName string) {
	if albumFocusStore == nil {
		return
	}
	// The config can be reloaded while Kiosk is running.
	albumFocusStore.Learn("", focusAlbums(baseConfig, baseConfig.Albums))
	if name := validSSEClientName(clientName); name != "_global" {
		albumFocusStore.Learn(name, focusAlbums(baseConfig, requestConfig.Albums))
	}
}

// applyAlbumFocus narrows the asset sources in requestConfig down to the
// focused album of clientName. It reports whether a focus was applied.
func applyAlbumFocus(requestConfig *config.Config, clientName string) bool {
	if albumFocusStore == nil {
		return false
	}
	album, ok := albumFocusStore.Active(clientName)
	if !ok {
		return false
	}

	// Only the album is shown; filters such as filter_date still apply.
	requestConfig.Albums = []string{album}
	requestConfig.People = nil
	requestConfig.Tags = nil
	requestConfig.Dates = nil
	requestConfig.Memories = false
	requestConfig.MemoriesOnly = false
	requestConfig.Rating = -1
	return true
}
