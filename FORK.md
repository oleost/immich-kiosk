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

- `internal/mqtt/mqtt.go`, `internal/routes/routes_sse.go`
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

### 4. Offline fallback for the installed PWA (`fix: serve offline fallback page for installed PWA when server is down`)

Upstream registers the service worker with no scope, so it only controls
`/assets/js/` and never intercepts page navigations. On an iOS "Add to Home
Screen" app a failed initial load then sticks on Safari's error page until the
shortcut is recreated.

This change registers the worker at root scope and serves a small self-retrying
offline page when the server is unreachable. The worker handles every "server
went away" flavour a reverse-proxied kiosk sees when the container
crashes/restarts:

- navigation fails outright (container down, connection refused);
- navigation hangs — connection accepted, no response ("the server stopped
  responding") — given up on after a 5s timeout instead of waiting for Safari's
  native timeout;
- navigation answers with a 5xx (502/503/504 from a reverse proxy while the
  container restarts, or 500 while the Go server boots) — treated as "not ready
  yet"; 4xx is passed through so auth prompts still show.

The offline page is embedded in the worker script as well, so a navigation is
always answered even when Cache Storage is empty or was evicted.

**In-page recovery (the important part for iOS).** An installed iOS PWA in
standalone mode does not route a *failed* top-level navigation through the
service worker, so `location.reload()` during an outage still lands on Safari's
native error screen. So for a kiosk that is already loaded and running, `kiosk.ts`
never reloads while the server is down: after a burst of failed/timed-out polls
it probes `/health` and, if that is unreachable, covers the still-rendered page
with a full-screen "Kiosk unavailable" overlay and polls `/health` from the live
document (also on `visibilitychange`/`pageshow`, since iOS freezes timers in the
background). Only once `/health` answers does it reload — a navigation that is
then guaranteed to succeed. If `/health` still answers during the failures the
fault is downstream (e.g. Immich): the last asset stays up with the offline
indicator and nothing reloads, so there is no reload loop.

- `frontend/public/assets/js/sw.js`, `frontend/public/assets/offline.html`
- `frontend/src/ts/kiosk.ts` (registration + cleanup of the old registration;
  in-page offline overlay + reload-when-healthy recovery)
- `main.go` (`/assets/js/sw.js` route with `Service-Worker-Allowed: /`)

Candidate for an upstream PR — it fixes a genuine upstream bug (installed kiosks
stuck on a blank screen after a container restart).
