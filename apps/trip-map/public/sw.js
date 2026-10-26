// Service worker: reminder push notifications (F12) and offline start of the world view (KON-23).
//
// Push: it only shows what the server sent and opens the app when a notification is tapped, and
// never touches footprint data.
//
// Offline: the page itself and the maps are always tried on the network first. Only when it fails
// does the worker serve from the same-origin cache `shanhai-world-shell-<id>` that the world view
// fills after it has loaded online: index.html for a navigation to the app's own address, and the
// hashed files under assets/ and the icon (cache first, a hashed name never changes its content;
// network when absent). maps/100000.json (also the China view's map) is served, when the network
// fails, from the data cache the shell's manifest names, and only if its bytes match the SHA-256 the
// manifest records. A shell cache counts only when its manifest is there (the page writes it last, so
// a download or write that failed is never used), and the newest such cache serves both index.html and
// the files, so the two never come from different builds. It does not look at /api/, other origins or
// non-GET requests, and it writes nothing: the page fills and cleans the caches.
// Cache lookups use `ignoreVary` because the page filled the cache with plain fetches while a
// module script or stylesheet asks with other headers.
const SHELL_PREFIX = "shanhai-world-shell-";
const DATA_PREFIX = "shanhai-world-data-";

/** The newest complete shell cache with its manifest, or null. */
async function completeShell() {
  const manifestUrl = new URL("shell-manifest.json", self.registration.scope).href;
  const names = (await caches.keys()).filter((name) => name.startsWith(SHELL_PREFIX));
  for (let i = names.length - 1; i >= 0; i--) {
    const cache = await caches.open(names[i]);
    const marker = await cache.match(manifestUrl, { ignoreVary: true });
    if (!marker) continue;
    try {
      return { cache, manifest: await marker.json() };
    } catch {
      // a manifest that cannot be read is no manifest
    }
  }
  return null;
}

async function sha256Hex(bytes) {
  const digest = new Uint8Array(await crypto.subtle.digest("SHA-256", bytes));
  return Array.from(digest, (byte) => byte.toString(16).padStart(2, "0")).join("");
}

/** The cached national map of the shell's own build, if its bytes are the ones the manifest recorded. */
async function cachedChinaMap(request) {
  const shell = await completeShell();
  const manifest = shell && shell.manifest;
  if (!manifest || typeof manifest.dataCache !== "string" || !manifest.dataCache.startsWith(DATA_PREFIX) || typeof manifest.chinaSha256 !== "string") return null;
  if (!(await caches.keys()).includes(manifest.dataCache)) return null;
  const data = await caches.open(manifest.dataCache);
  const hit = await data.match(request, { ignoreVary: true });
  if (!hit) return null;
  const bytes = await hit.clone().arrayBuffer();
  return (await sha256Hex(bytes)) === manifest.chinaSha256 ? hit : null;
}

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
      fetch(request).catch(async () => {
        const shell = await completeShell().catch(() => null);
        return (shell && (await shell.cache.match(scopePath, { ignoreVary: true }))) || Response.error();
      }),
    );
  } else if (url.pathname.startsWith(`${scopePath}assets/`) || url.pathname === `${scopePath}favicon.svg`) {
    // Hashed file names never change their content, so a cached copy is as good as the network's.
    event.respondWith(
      completeShell()
        .catch(() => null)
        .then((shell) => shell && shell.cache.match(request, { ignoreVary: true }))
        .then((hit) => hit || fetch(request)),
    );
  } else if (url.pathname === `${scopePath}maps/100000.json`) {
    event.respondWith(fetch(request).catch(async () => (await cachedChinaMap(request).catch(() => null)) || Response.error()));
  }
});
