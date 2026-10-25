# Fork notes

This is a fork of [damongolding/immich-kiosk](https://github.com/damongolding/immich-kiosk).
It tracks upstream closely and carries a small set of extra changes on top.

**Using the fork features** (MQTT, Home Assistant, text messages, override,
Home Assistant controls):
see [MQTT.md](MQTT.md). This file describes the changes for maintaining the fork.

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
  the description is empty or duplicated) plus `off`. Raw `start` commands
  match the description first, then the filename.
- Image lists are loaded via Immich's search API (trashed images skipped) and
  refreshed every `override_source_refresh_interval` minutes, or on
  `refresh-sources`.
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

### 5. Timed text messages via MQTT (`feat: show timed text messages via MQTT`)

Shows a text banner on top of the slideshow on all screens or one screen.
Builds on the MQTT feature (1).

- Each HA device gets a `text` (message), a `number` (duration in minutes) and
  `Message: send` / `Message: stop` buttons. The text and duration are kept as
  retained MQTT states, so they survive a restart.
- Raw commands on `<prefix>[/<client>]/message/command`: `show`, `clear`, plain
  text, or JSON with `text`, `duration`, `position`, `size`.
- Messages live in memory, expire after their duration, and are sent to the
  browser as a `kiosk-message` event on `/events`, including on (re)connect so
  a reload keeps the message. The text is rendered with `textContent`.
- `message` is reserved as a client name, like `override`.

Files:
- `internal/message/message.go` (new), `internal/routes/routes_message.go` (new)
- `internal/mqtt/message.go` (new), `internal/mqtt/mqtt.go`
- `internal/routes/routes_mqtt_sse.go` (events carry a name)
- `frontend/src/ts/mqtt-message.ts`, `frontend/src/css/kiosk-message.css` (new),
  `frontend/src/ts/mqtt-sse.ts`, `frontend/src/css/kiosk.css` (import)
- `config.example.yaml`, `main.go`

### 6. HA entity polish and override duration (`feat: add override duration and tidy Home Assistant entities`)

- Text and duration fields get a default state (empty text, 5 min) when no
  retained state exists, so Home Assistant no longer shows them as "unknown".
  Stored values are republished when Home Assistant restarts
  (`homeassistant/status` = `online`).
- New `Override: duration` number (minutes) per device; `override_max_duration`
  is now its default and maximum. Topic `<prefix>[/<client>]/override/duration/set`,
  so `duration` is a reserved source key.
- Clients are also learned from their retained override states, so a screen
  that is not connected still gets the new image list when it changes.
- Entity names are prefixed (`Image:`, `Message:`, `Override:`) so they group
  on the device page; text and duration inputs use `entity_category: config`.

Files:
- `internal/mqtt/drafts.go` (new), `internal/mqtt/message.go`, `internal/mqtt/mqtt.go`
- `internal/override/override.go`, `internal/routes/routes_override.go`
- `internal/config/config.go`, `config.example.yaml`

### 7. User guide (`docs: add MQTT and Home Assistant user guide`)

`MQTT.md` explains setup, screen names, the HA entities, text messages,
override, HA automation examples and the topic reference. The schema now
rejects the reserved override source key `duration`.

- `MQTT.md` (new), `FORK.md`, `config.example.yaml`, `config.schema.json`

### 8. Fork README (`docs: add fork README`)

`.github/README.md` is the README GitHub shows for this fork (it takes
precedence over the root `README.md`, which stays identical to upstream so
rebases never conflict on it). It marks the fork as unofficial and AI-assisted,
lists the additions, and credits upstream.

- `.github/README.md` (new), `FORK.md`
- `.github/assets/home-assistant-device.png`: screenshot of the Home Assistant
  device page shown in the README
- `.github/ISSUE_TEMPLATE/config.yml`: blank issues allowed, and the upstream
  "Feature Request" discussion link replaced with the upstream docs (issues are
  enabled on the fork)

### 9. Home Assistant controls (`feat: control the slideshow from Home Assistant`)

Selects, switches, a button and sensors on every HA device, with no time
limit. Builds on the MQTT feature (1).

- **`Album: show` / `Person: show`** (selects): narrow the slideshow down to
  one album or person. Options: `Default` plus the albums/people from
  `config.yaml` on the "all" device, and those a client requests (URL or
  config) on a client device. Labels are the Immich names, made unique;
  keywords get fixed labels, unnamed items `Unnamed (<id start>)`. A device
  with fewer than two items gets no select. Album and person exclude each
  other on one device. `internal/focus` holds the lists and selections.
- **`albums_on_demand`** (top-level, next to `albums`): extra album options on
  every device that are never part of `Default`. A separate key rather than a
  marker inside `albums`, so no upstream code that reads `albums` changes. Not
  settable via URL (`?album=<id>` already shows any album).
- **Switches** (`internal/switches`): `Slideshow: pause`, `Slideshow: sleep`,
  `Filter: favorites`, `Filter: memories`. On for "all" applies to every
  client; a client switch can add, not cancel.
  - Pause and sleep reach the browser as a `kiosk-control` SSE event (also on
    connect). Pause holds across next/previous (`frontend/src/ts/mqtt-control.ts`
    re-pauses after each asset). Sleep makes `/asset/new`, `/asset/previous`
    and `/sleep` report sleep; the browser asks `/sleep`, so upstream's sleep
    mode, icon and screen dimming are used unchanged. `/sleep` only checks the
    schedule when one is set and `disable_sleep` is off.
  - Favorites sets `filter_favorites`; memories switches to memories only when
    Immich has memories for today. If the controls leave nothing to show, the
    request is retried with the normal sources.
- **`Page: reload`** button: `reload` on the navigation command topic, sent
  only to connected clients (never queued, or a reconnecting page would
  reload again).
- **`Image: current`** sensor (state + JSON attributes) and **`Image: preview`**
  image (JPEG, max 640 px), published per client and on the "all" device
  (last image anywhere) whenever a new or history asset is served.
- Client lists are learned from asset requests (in memory). Selections and
  switches are retained states, restored on startup; default states are
  published 3 s after discovery so they cannot overwrite a retained state
  that has not arrived yet.
- An override wins over all controls. While a control is active the prefetch
  cache is bypassed, as for overrides.

Files:
- `internal/focus/` (new, + tests), `internal/switches/` (new, + tests)
- `internal/mqtt/controls.go`, `internal/mqtt/now_showing.go` (new),
  `internal/mqtt/mqtt.go` (+ `mqtt_test.go`)
- `internal/routes/routes_controls.go`, `internal/routes/routes_now_showing.go`
  (new, + `routes_controls_test.go`), `internal/routes/routes_mqtt_sse.go`
- `internal/routes/routes_asset.go`, `internal/routes/routes_previous_asset.go`,
  `internal/routes/routes_sleep.go` (small upstream-file changes)
- `frontend/src/ts/mqtt-control.ts` (new), `frontend/src/ts/mqtt-sse.ts`
- `internal/config/config.go`, `config.schema.json` (`albums_on_demand`), `main.go`
- `MQTT.md`, `.github/README.md`, `config.example.yaml`

## Previously carried, now upstream

- **Offline fallback / auto-reconnect for the installed PWA.** Dropped when
  syncing to v0.44.1: upstream v0.44.0 ships its own root-scoped service worker,
  `/recover` page and in-page recovery overlay (`frontend/src/ts/recovery.ts`).
