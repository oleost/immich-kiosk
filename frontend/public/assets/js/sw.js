// Immich Kiosk service worker.
//
// Its job is to keep the installed PWA usable when the Kiosk server is
// unreachable. iOS "Add to Home Screen" apps get permanently stuck on Safari's
// native error page when the initial navigation fails; serving a small
// self-retrying offline page instead lets the app recover on its own once the
// server is back.
//
// This script must be registered with an explicit scope of "/" (the server
// sends a `Service-Worker-Allowed: /` header) so it can intercept top-level
// navigations and not just requests under /assets/js/.

var CACHE = "immich-kiosk-v2";
var OFFLINE_URL = "/assets/offline.html";

self.addEventListener("install", function (event) {
    event.waitUntil(
        caches.open(CACHE).then(function (cache) {
            // offline.html is an embedded static asset and must always cache,
            // even if the app server is down while the worker installs.
            return cache.add(OFFLINE_URL);
        }),
    );
    self.skipWaiting();
});

self.addEventListener("activate", function (event) {
    event.waitUntil(
        caches
            .keys()
            .then(function (keys) {
                return Promise.all(
                    keys
                        .filter(function (key) {
                            return key !== CACHE;
                        })
                        .map(function (key) {
                            return caches.delete(key);
                        }),
                );
            })
            .then(function () {
                return self.clients.claim();
            }),
    );
});

self.addEventListener("fetch", function (event) {
    var request = event.request;

    // Only step in for top-level navigations. When the server cannot be reached
    // serve the offline page, which polls /health and reloads once Kiosk is back.
    if (request.mode !== "navigate") {
        return;
    }

    event.respondWith(
        fetch(request).catch(function () {
            return caches.match(OFFLINE_URL, { ignoreSearch: true });
        }),
    );
});
