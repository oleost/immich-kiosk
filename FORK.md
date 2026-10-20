# Fork notes

This is a fork of [damongolding/immich-kiosk](https://github.com/damongolding/immich-kiosk).
It tracks upstream closely and carries a small set of extra changes on top.

## How this fork stays in sync with upstream

`main` is kept as **`upstream/main` + the fork commits listed below**, applied by
rebase (not merge):

```sh
git fetch upstream
git rebase upstream/main        # replay the fork commits on top of upstream
go tool templ generate          # regenerate *_templ.go after the rebase
task build                      # sanity-check the build
git push --force-with-lease origin main
```

A force-push to `origin/main` is expected after every sync. Pushing to
`origin/main` builds and publishes `ghcr.io/oleost/immich-kiosk:latest` via
`.github/workflows/docker-publish.yml`.

## Fork-specific changes

Listed oldest first — this is also the order they replay during a rebase.

### 1. MQTT remote control (`feat: add MQTT support for remote control via Home Assistant`)

An MQTT client that subscribes to navigation commands and broadcasts them to
connected browsers over SSE, so kiosk instances can be driven from Home
Assistant. Opt-in via config.

- `internal/mqtt/mqtt.go`, `internal/routes/routes_mqtt_sse.go` (own file so
  upstream's `routes_sse.go` stays untouched)
- `internal/config/config.go`, `config.example.yaml`, `config.schema.json`
- `frontend/src/ts/mqtt-sse.ts`, wired in `frontend/src/ts/kiosk.ts`
- `main.go` (client startup), `go.mod` / `go.sum` (MQTT library)

### 2. Preserve `client` query param in the PWA manifest (`fix: preserve client query parameter in PWA manifest start_url`)

`start_url` in the generated manifest keeps the `client` query parameter (and
only that one) so an installed PWA launches with its configured client id.

- `internal/routes/routes_manifest.go`

### 3. Publish Docker image on `main` push (`ci: build and publish Docker image to ghcr.io/oleost on main pushes`)

Fork-only workflow that builds a multi-arch image and pushes
`ghcr.io/oleost/immich-kiosk` (`:latest`, `:main`) on every push to `main`.
Upstream only publishes on version tags.

- `.github/workflows/docker-publish.yml`

### 4. Timed override via MQTT (`feat: add timed album override via MQTT`)

Temporarily replaces the slideshow on all screens or one screen (e.g. show a
recipe for two hours). Builds on the MQTT feature (1).

- Only albums listed in `kiosk.override_sources` can be shown.
- Each source becomes a Home Assistant `select` on the "all" device and on each
  client device; options are each image's Immich description (filename when
  the description is empty or duplicated) plus `off`.
- Image lists are loaded via Immich's search API and refreshed every
  `override_source_refresh_interval` minutes, or on `refresh-sources`.
- Overrides live in memory and expire after `override_max_duration`; the screen
  is told to move on immediately on start, stop and expiry.
- While an override is active the prefetch cache is bypassed (its key does not
  include override state).

Files:
- `internal/override/override.go` (new), `internal/routes/routes_override.go` (new)
- `internal/mqtt/mqtt.go` (+ `mqtt_test.go`)
- `internal/routes/routes_asset.go`, `internal/routes/routes_asset_helpers.go`
  (`PinnedAssetID` short-circuit in `processAsset`, falls back to the album
  if the pinned image is gone)
- `internal/config/config.go`, `config.example.yaml`, `config.schema.json`
- `main.go`

## Previously carried, now upstream

- **Offline fallback / auto-reconnect for the installed PWA.** Dropped when
  syncing to v0.44.1: upstream v0.44.0 ships its own root-scoped service worker,
  `/recover` page and in-page recovery overlay (`frontend/src/ts/recovery.ts`).
