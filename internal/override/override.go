// Package override manages timed album overrides for Immich Kiosk.
//
// An override replaces the normal asset sources for one or all connected clients
// for a limited time. Only albums pre-configured in kiosk.override_sources can be
// used. Asset labels (the Immich description, or the filename when there is none)
// for each source are cached so Home Assistant can offer them as select options;
// the cache is refreshed periodically and on demand.
package override

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"charm.land/log/v2"

	"github.com/damongolding/immich-kiosk/internal/config"
	"github.com/damongolding/immich-kiosk/internal/immich"
)

const (
	expiryCheckInterval = 15 * time.Second
	maxSearchPages      = 50
	searchPageSize      = 1000
	// Home Assistant rejects entity states longer than 255 characters.
	maxLabelLength = 255
)

// AssetEntry is a single asset in an override source's cache.
type AssetEntry struct {
	ID           string
	OriginalName string
	// Label is what Home Assistant shows: the Immich description, or the
	// filename when the description is empty or already taken.
	Label string
}

// State is an active override for one target.
type State struct {
	SourceKey string
	AlbumID   string
	// AssetID is empty when the whole album is shown.
	AssetID   string
	AssetName string
	ExpiresAt time.Time
}

// Store holds active overrides and the asset-name cache.
type Store struct {
	mu        sync.Mutex
	overrides map[string]*State // target ("" = all clients) -> state

	cacheMu    sync.RWMutex
	assetCache map[string][]AssetEntry // source key -> assets

	sources      []config.OverrideSource
	maxDuration  time.Duration
	refreshEvery time.Duration

	immichURL  string
	apiKey     string
	httpClient *http.Client

	onCacheRefresh func(sourceKey string)
	onExpire       func(target string, st State)
}

// New creates a Store from the kiosk settings.
func New(immichURL, apiKey string, settings config.KioskSettings) *Store {
	return &Store{
		overrides:    make(map[string]*State),
		assetCache:   make(map[string][]AssetEntry),
		sources:      settings.OverrideSources,
		maxDuration:  time.Duration(settings.OverrideMaxDuration) * time.Second,
		refreshEvery: time.Duration(settings.OverrideSourceRefreshInterval) * time.Minute,
		immichURL:    immichURL,
		apiKey:       apiKey,
		httpClient:   &http.Client{Timeout: time.Duration(settings.HTTPTimeout) * time.Second},
	}
}

// MaxDuration returns the default and longest override duration.
func (s *Store) MaxDuration() time.Duration {
	return s.maxDuration
}

// Sources returns the configured override sources.
func (s *Store) Sources() []config.OverrideSource {
	return s.sources
}

// SetOnCacheRefresh registers a callback run after a source's asset list changes.
// Must be called before Start.
func (s *Store) SetOnCacheRefresh(fn func(sourceKey string)) {
	s.onCacheRefresh = fn
}

// SetOnExpire registers a callback run when an override expires.
// Must be called before Start.
func (s *Store) SetOnExpire(fn func(target string, st State)) {
	s.onExpire = fn
}

// Start loads the asset cache and runs the refresh and expiry loops in the background.
func (s *Store) Start(ctx context.Context) {
	go func() {
		s.RefreshAll()

		expiry := time.NewTicker(expiryCheckInterval)
		defer expiry.Stop()

		var refresh <-chan time.Time
		if s.refreshEvery > 0 {
			t := time.NewTicker(s.refreshEvery)
			defer t.Stop()
			refresh = t.C
		}

		for {
			select {
			case <-ctx.Done():
				return
			case <-refresh:
				s.RefreshAll()
			case <-expiry.C:
				s.expire()
			}
		}
	}()
}

// RefreshAll refreshes the asset cache for every source.
func (s *Store) RefreshAll() {
	for _, src := range s.sources {
		s.refreshSource(src)
	}
}

// RefreshSource refreshes the asset cache for one source key.
func (s *Store) RefreshSource(key string) {
	src, ok := s.SourceByKey(key)
	if !ok {
		log.Warn("override: unknown source", "key", key)
		return
	}
	s.refreshSource(src)
}

func (s *Store) refreshSource(src config.OverrideSource) {
	assets, err := s.fetchAlbumAssets(src.Album)
	if err != nil {
		log.Error("override: fetching album assets", "source", src.Key, "err", err)
		return
	}

	s.cacheMu.Lock()
	changed := !slices.Equal(s.assetCache[src.Key], assets)
	s.assetCache[src.Key] = assets
	s.cacheMu.Unlock()

	if !changed {
		return
	}

	log.Warn("override: asset list updated", "source", src.Key, "assets", len(assets))

	if s.onCacheRefresh != nil {
		s.onCacheRefresh(src.Key)
	}
}

// AssetNames returns the cached labels for a source, in display order.
func (s *Store) AssetNames(key string) []string {
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()
	names := make([]string, len(s.assetCache[key]))
	for i, a := range s.assetCache[key] {
		names[i] = a.Label
	}
	return names
}

// ResolveAssetName finds an asset in a source by label, falling back to
// filename (both case-insensitive).
func (s *Store) ResolveAssetName(key, name string) (AssetEntry, bool) {
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()
	for _, a := range s.assetCache[key] {
		if strings.EqualFold(a.Label, name) {
			return a, true
		}
	}
	for _, a := range s.assetCache[key] {
		if strings.EqualFold(a.OriginalName, name) {
			return a, true
		}
	}
	return AssetEntry{}, false
}

// SourceByKey returns the configured source with the given key.
func (s *Store) SourceByKey(key string) (config.OverrideSource, bool) {
	for _, src := range s.sources {
		if src.Key == key {
			return src, true
		}
	}
	return config.OverrideSource{}, false
}

// Activate starts an override for target ("" = all clients) using a configured source.
// assetID must come from ResolveAssetName, or be empty to show the whole album.
// A duration of zero or above the maximum is replaced by the maximum.
func (s *Store) Activate(target, sourceKey, assetID, assetName string, duration time.Duration) error {
	src, ok := s.SourceByKey(sourceKey)
	if !ok {
		return fmt.Errorf("unknown override source %q", sourceKey)
	}

	st := &State{
		SourceKey: sourceKey,
		AlbumID:   src.Album,
		AssetID:   assetID,
		AssetName: assetName,
		ExpiresAt: time.Now().Add(clampDuration(duration, s.maxDuration)),
	}

	s.mu.Lock()
	s.overrides[target] = st
	s.mu.Unlock()

	log.Warn("override: activated", "source", sourceKey, "asset", assetName, "target", target, "until", st.ExpiresAt.Format(time.TimeOnly))
	return nil
}

func clampDuration(d, maxDuration time.Duration) time.Duration {
	if d <= 0 || d > maxDuration {
		return maxDuration
	}
	return d
}

// Deactivate clears the override for target.
func (s *Store) Deactivate(target string) {
	s.mu.Lock()
	delete(s.overrides, target)
	s.mu.Unlock()
	log.Warn("override: stopped", "target", target)
}

// Active returns the override that applies to clientName: a per-client override
// takes priority over a global one. Returns nil when none is active.
func (s *Store) Active(clientName string) *State {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	if clientName != "" {
		if st, ok := s.overrides[clientName]; ok && now.Before(st.ExpiresAt) {
			return st
		}
	}
	if st, ok := s.overrides[""]; ok && now.Before(st.ExpiresAt) {
		return st
	}
	return nil
}

// Current returns the active override set for exactly target (no global fallback).
func (s *Store) Current(target string) *State {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.overrides[target]; ok && time.Now().Before(st.ExpiresAt) {
		return st
	}
	return nil
}

func (s *Store) expire() {
	now := time.Now()
	expired := map[string]State{}

	s.mu.Lock()
	for target, st := range s.overrides {
		if !now.Before(st.ExpiresAt) {
			expired[target] = *st
			delete(s.overrides, target)
		}
	}
	s.mu.Unlock()

	for target, st := range expired {
		log.Warn("override: expired", "source", st.SourceKey, "target", target)
		if s.onExpire != nil {
			s.onExpire(target, st)
		}
	}
}

// fetchAlbumAssets lists the images in an album via Immich's search API,
// the same endpoint Kiosk uses for album playback.
func (s *Store) fetchAlbumAssets(albumID string) ([]AssetEntry, error) {
	u, err := url.Parse(s.immichURL)
	if err != nil {
		return nil, fmt.Errorf("parse immich url: %w", err)
	}
	apiURL := url.URL{Scheme: u.Scheme, Host: u.Host, Path: immich.SearchMetadataEndpoint}

	body := immich.SearchRandomBody{
		Filter:   immich.NewSearchFilterBuilder().WithAlbumsAny(albumID).Build(),
		Size:     searchPageSize,
		WithExif: true,
	}

	// "off" is the select option that stops the override.
	seen := map[string]bool{"off": true}
	var entries []AssetEntry

	for range maxSearchPages {
		page, err := s.searchPage(apiURL.String(), body)
		if err != nil {
			return nil, err
		}

		for _, a := range page.Assets.Items {
			// The search API also returns trashed assets.
			if a.IsTrashed {
				continue
			}

			// HA select options must be unique: fall back to the filename when the
			// description is empty or taken, and skip the asset if both are.
			label := assetLabel(a.ExifInfo.Description)
			if label == "" || seen[strings.ToLower(label)] {
				label = a.OriginalFileName
			}
			if label == "" || seen[strings.ToLower(label)] {
				continue
			}
			seen[strings.ToLower(label)] = true
			entries = append(entries, AssetEntry{ID: a.ID, OriginalName: a.OriginalFileName, Label: label})
		}

		if page.Assets.NextCursor == "" {
			break
		}
		body.Cursor = page.Assets.NextCursor
	}

	sort.Slice(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].Label) < strings.ToLower(entries[j].Label)
	})
	return entries, nil
}

// assetLabel turns an Immich description into a single-line select option.
func assetLabel(description string) string {
	label := strings.Join(strings.Fields(description), " ")
	if r := []rune(label); len(r) > maxLabelLength {
		label = strings.TrimSpace(string(r[:maxLabelLength-1])) + "…"
	}
	return label
}

func (s *Store) searchPage(apiURL string, body immich.SearchRandomBody) (immich.SearchMetadataResponse, error) {
	var res immich.SearchMetadataResponse

	payload, err := json.Marshal(body)
	if err != nil {
		return res, err
	}

	req, err := http.NewRequest(http.MethodPost, apiURL, bytes.NewReader(payload))
	if err != nil {
		return res, err
	}
	req.Header.Set("x-api-key", s.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return res, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return res, fmt.Errorf("immich returned %s", resp.Status)
	}

	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return res, fmt.Errorf("decode search response: %w", err)
	}
	return res, nil
}
