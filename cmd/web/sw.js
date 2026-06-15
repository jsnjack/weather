const SHELL_CACHE = "forecast-shell-v5";
const DATA_CACHE  = "forecast-data-v5";
const SHELL = ["/static/styles.css", "/manifest.webmanifest", "/icon.svg"];

self.addEventListener("install", (event) => {
  event.waitUntil(caches.open(SHELL_CACHE).then((c) => c.addAll(SHELL)));
  self.skipWaiting();
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches.keys().then((keys) =>
      Promise.all(keys.filter((k) => k !== SHELL_CACHE && k !== DATA_CACHE).map((k) => caches.delete(k)))
    )
  );
  self.clients.claim();
});

self.addEventListener("fetch", (event) => {
  const req = event.request;
  if (req.method !== "GET") return;
  const url = new URL(req.url);

  // Network-first for page navigations (/, /hourly, /forecast, /today,
  // /multiday), the API, and the live radar GIF; fall back to the last good
  // response when offline. The radar loop refreshes upstream every few
  // minutes, so it must never be served from the long-lived shell cache —
  // stale-while-revalidate there pinned yesterday's frame and a manual
  // refresh just re-served the same stale copy.
  if (req.mode === "navigate" || url.pathname.startsWith("/api/") || url.pathname === "/radar.gif") {
    event.respondWith(
      fetch(req)
        .then((res) => {
          if (res && res.ok) {
            const copy = res.clone();
            caches.open(DATA_CACHE).then((c) => c.put(req, copy));
          }
          return res;
        })
        .catch(() => caches.match(req).then((hit) => hit || new Response("offline", { status: 503 })))
    );
    return;
  }

  // Stale-while-revalidate for the static shell: serve the cached copy for
  // speed but always refetch in the background, so the next load is fresh.
  // Cache-first once pinned an old styles.css forever — new HTML rendered
  // by months-old CSS (vanished legend swatches, wrong background).
  event.respondWith(
    caches.open(SHELL_CACHE).then(async (c) => {
      const hit = await c.match(req);
      const refresh = fetch(req).then((res) => {
        if (res && res.ok) c.put(req, res.clone());
        return res;
      });
      if (hit) {
        event.waitUntil(refresh.catch(() => {}));
        return hit;
      }
      return refresh;
    })
  );
});
