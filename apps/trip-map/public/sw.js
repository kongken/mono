// Service worker: reminder push notifications (F12) and offline start of the world view (KON-23).
//
// Push: it only shows what the server sent and opens the app when a notification is tapped, and
// never touches footprint data.
//
// Offline: the page itself and the maps are always tried on the network first. Only when it fails
// does the worker serve, from the same-origin cache `shanhai-world-shell` that the world view fills
// after it has loaded online, index.html for a navigation to the app's own address; maps/100000.json
// (also the China view's map) falls back to the copy the world view keeps in `shanhai-world-data-*`.
// The hashed files under assets/ and the icon are served from that cache when present (a hashed
// name never changes its content) and from the network otherwise. It does not look at /api/, other
// origins or non-GET requests, and it writes nothing: the page fills and cleans both caches.
// Cache lookups use `ignoreVary` because the page filled the cache with plain fetches while a
// module script or stylesheet asks with other headers.
const SHELL_CACHE = "shanhai-world-shell";
const DATA_CACHE_PREFIX = "shanhai-world-data-";

self.addEventListener("install", () => self.skipWaiting());
self.addEventListener("activate", (event) => event.waitUntil(self.clients.claim()));

self.addEventListener("push", (event) => {
  let data = {};
  try {
    data = event.data ? event.data.json() : {};
  } catch {
    data = {};
  }
  const title = typeof data.title === "string" && data.title ? data.title : "山海足迹";
  const options = {
    body: typeof data.body === "string" ? data.body : "",
    tag: typeof data.tag === "string" && data.tag ? data.tag : "tripmap",
    icon: "/favicon.svg",
    data: { url: typeof data.url === "string" ? data.url : "/" },
  };
  event.waitUntil(self.registration.showNotification(title, options));
});

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  // Only same-origin targets are opened, whatever the payload says.
  let target = new URL("/", self.location.origin);
  try {
    const wanted = new URL(event.notification.data && event.notification.data.url, self.location.origin);
    if (wanted.origin === self.location.origin) target = wanted;
  } catch {
    // keep the default
  }
  event.waitUntil(
    self.clients.matchAll({ type: "window", includeUncontrolled: true }).then((windows) => {
      for (const client of windows) {
        if (new URL(client.url).origin === self.location.origin && "focus" in client) return client.focus();
      }
      return self.clients.openWindow(target.href);
    }),
  );
});

self.addEventListener("fetch", (event) => {
  const request = event.request;
  if (request.method !== "GET") return;
  const url = new URL(request.url);
  if (url.origin !== self.location.origin) return;
  const scopePath = new URL(self.registration.scope).pathname;
  if (request.mode === "navigate") {
    if (url.pathname !== scopePath) return;
    event.respondWith(
      fetch(request).catch(async () => (await caches.match(scopePath, { cacheName: SHELL_CACHE, ignoreVary: true })) || Response.error()),
    );
  } else if (url.pathname.startsWith(`${scopePath}assets/`) || url.pathname === `${scopePath}favicon.svg`) {
    // Hashed file names never change their content, so a cached copy is as good as the network's.
    event.respondWith(
      caches.match(request, { cacheName: SHELL_CACHE, ignoreVary: true }).then((hit) => hit || fetch(request)),
    );
  } else if (url.pathname === `${scopePath}maps/100000.json`) {
    event.respondWith(
      fetch(request).catch(async () => {
        for (const name of await caches.keys()) {
          if (!name.startsWith(DATA_CACHE_PREFIX)) continue;
          const hit = await caches.match(request, { cacheName: name, ignoreVary: true });
          if (hit) return hit;
        }
        return Response.error();
      }),
    );
  }
});
