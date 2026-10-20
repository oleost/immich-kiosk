package routes

import (
	"context"

	"charm.land/log/v2"

	"github.com/damongolding/immich-kiosk/internal/config"
	"github.com/damongolding/immich-kiosk/internal/mqtt"
	"github.com/damongolding/immich-kiosk/internal/override"
)

// overrideStore holds active overrides; nil when no override sources are configured.
var overrideStore *override.Store

// SetupOverride creates the override store and, when MQTT is available, exposes
// it to Home Assistant. mqttClient may be nil.
func SetupOverride(ctx context.Context, baseConfig *config.Config, mqttClient *mqtt.Client) {
	store := override.New(baseConfig.ImmichURL, baseConfig.ImmichAPIKey, baseConfig.Kiosk)
	overrideStore = store

	if mqttClient == nil {
		log.Warn("override_sources configured but MQTT is not connected; overrides cannot be activated")
	} else {
		store.SetOnCacheRefresh(func(string) { mqttClient.PublishOverrideDiscovery() })
		store.SetOnExpire(func(target string, _ override.State) {
			sseHub.broadcast(target, string(mqtt.CommandNext))
			mqttClient.PublishOverrideState(target)
		})

		mqttClient.EnableOverride(mqtt.OverrideProvider{
			Sources:    store.Sources(),
			AssetNames: store.AssetNames,
			Current: func(target string) (string, string, bool) {
				st := store.Current(target)
				if st == nil {
					return "", "", false
				}
				return st.SourceKey, st.AssetName, true
			},
			Clients: connectedClients,
			Handle:  func(cmd mqtt.OverrideCommand) { handleOverrideCommand(store, mqttClient, cmd) },
		})
	}

	store.Start(ctx)
	log.Warn("Override enabled", "sources", len(store.Sources()))
}

func handleOverrideCommand(store *override.Store, mqttClient *mqtt.Client, cmd mqtt.OverrideCommand) {
	switch cmd.Action {
	case mqtt.OverrideStart:
		if _, ok := store.SourceByKey(cmd.SourceKey); !ok {
			log.Warn("override: unknown source", "key", cmd.SourceKey)
			return
		}

		var assetID, assetName string
		if cmd.AssetName != "" {
			entry, ok := store.ResolveAssetName(cmd.SourceKey, cmd.AssetName)
			if !ok {
				// The image may have been added since the last refresh.
				store.RefreshSource(cmd.SourceKey)
				entry, ok = store.ResolveAssetName(cmd.SourceKey, cmd.AssetName)
			}
			if !ok {
				log.Warn("override: image not found in source", "source", cmd.SourceKey, "name", cmd.AssetName)
				mqttClient.PublishOverrideState(cmd.Target)
				return
			}
			assetID, assetName = entry.ID, entry.Label
		}

		if err := store.Activate(cmd.Target, cmd.SourceKey, assetID, assetName); err != nil {
			log.Error("override: activate", "err", err)
			return
		}

	case mqtt.OverrideStop:
		// A select set to "off" only stops the override it controls.
		if cmd.SourceKey != "" {
			if st := store.Current(cmd.Target); st == nil || st.SourceKey != cmd.SourceKey {
				mqttClient.PublishOverrideState(cmd.Target)
				return
			}
		}
		store.Deactivate(cmd.Target)

	case mqtt.OverrideRefresh:
		if cmd.SourceKey != "" {
			store.RefreshSource(cmd.SourceKey)
		} else {
			store.RefreshAll()
		}
		mqttClient.PublishOverrideDiscovery()
		return
	}

	sseHub.broadcast(cmd.Target, string(mqtt.CommandNext))
	mqttClient.PublishOverrideState(cmd.Target)
}

// connectedClients returns the names of the currently connected SSE clients.
func connectedClients() []string {
	sseHub.mu.RLock()
	defer sseHub.mu.RUnlock()
	names := make([]string, 0, len(sseHub.knownClients))
	for name := range sseHub.knownClients {
		names = append(names, name)
	}
	return names
}

// applyOverride replaces the asset sources in requestConfig with the active
// override for clientName. It reports whether an override was applied.
func applyOverride(requestConfig *config.Config, clientName string) bool {
	if overrideStore == nil {
		return false
	}
	st := overrideStore.Active(clientName)
	if st == nil {
		return false
	}

	requestConfig.Albums = []string{st.AlbumID}
	requestConfig.People = nil
	requestConfig.Tags = nil
	requestConfig.Dates = nil
	requestConfig.Memories = false
	requestConfig.MemoriesOnly = false
	requestConfig.Rating = -1
	requestConfig.FilterFavorites = false
	requestConfig.FilterNewest = 0
	requestConfig.FilterDate = ""
	requestConfig.PinnedAssetID = st.AssetID
	return true
}
