/* Beauty PWA demo Service Worker
 * - App Shell: cache-first for / and /static/*
 * - API public: stale-while-revalidate（配合服务端 Cache-Control）
 * - 写操作失败：写入 IndexedDB，online 后 POST /sync 批量回放
 */
const SHELL = "beauty-shell-v1";
const SHELL_URLS = ["/", "/static/app.js", "/static/manifest.webmanifest", "/static/icon.svg"];

self.addEventListener("install", (event) => {
  event.waitUntil(caches.open(SHELL).then((c) => c.addAll(SHELL_URLS)).then(() => self.skipWaiting()));
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches.keys().then((keys) =>
      Promise.all(keys.filter((k) => k !== SHELL).map((k) => caches.delete(k)))
    ).then(() => self.clients.claim())
  );
});

self.addEventListener("fetch", (event) => {
  const req = event.request;
  const url = new URL(req.url);
  if (req.method === "GET" && (url.pathname === "/" || url.pathname.startsWith("/static/"))) {
    event.respondWith(cacheFirst(req));
    return;
  }
  if (req.method === "GET" && url.pathname.startsWith("/api/public/")) {
    event.respondWith(staleWhileRevalidate(req));
    return;
  }
  if (req.method === "POST" && url.pathname.startsWith("/api/") && url.pathname !== "/sync") {
    event.respondWith(networkWithQueue(req));
  }
});

async function cacheFirst(req) {
  const cached = await caches.match(req);
  return cached || fetch(req);
}

async function staleWhileRevalidate(req) {
  const cache = await caches.open(SHELL);
  const cached = await cache.match(req);
  const network = fetch(req).then((res) => {
    if (res.ok) cache.put(req, res.clone());
    return res;
  });
  return cached || network;
}

async function networkWithQueue(req) {
  try {
    const res = await fetch(req.clone());
    if (res.ok) return res;
    throw new Error("status " + res.status);
  } catch (err) {
    const body = await req.clone().text();
    const key = req.headers.get("Idempotency-Key") || crypto.randomUUID();
    await enqueue({
      id: key,
      method: req.method,
      path: new URL(req.url).pathname,
      body: body ? JSON.parse(body) : null,
    });
    return new Response(JSON.stringify({ queued: true, id: key }), {
      status: 202,
      headers: { "Content-Type": "application/json" },
    });
  }
}

self.addEventListener("sync", (event) => {
  if (event.tag === "beauty-offline-sync") {
    event.waitUntil(flushQueue());
  }
});

self.addEventListener("push", (event) => {
  let data = { title: "Beauty", body: "push" };
  try {
    if (event.data) data = event.data.json();
  } catch (_) {}
  event.waitUntil(self.registration.showNotification(data.title || "Beauty", {
    body: data.body || "",
    data: data,
  }));
});

const DB = "beauty-offline";
const STORE = "ops";

function idb() {
  return new Promise((resolve, reject) => {
    const req = indexedDB.open(DB, 1);
    req.onupgradeneeded = () => req.result.createObjectStore(STORE, { keyPath: "id" });
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
}

async function enqueue(op) {
  const db = await idb();
  await new Promise((resolve, reject) => {
    const tx = db.transaction(STORE, "readwrite");
    tx.objectStore(STORE).put(op);
    tx.oncomplete = resolve;
    tx.onerror = () => reject(tx.error);
  });
  if (self.registration && self.registration.sync) {
    try { await self.registration.sync.register("beauty-offline-sync"); } catch (_) {}
  }
}

async function flushQueue() {
  const db = await idb();
  const ops = await new Promise((resolve, reject) => {
    const tx = db.transaction(STORE, "readonly");
    const req = tx.objectStore(STORE).getAll();
    req.onsuccess = () => resolve(req.result || []);
    req.onerror = () => reject(req.error);
  });
  if (!ops.length) return;
  const res = await fetch("/sync", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ ops }),
  });
  if (!res.ok) return;
  await new Promise((resolve, reject) => {
    const tx = db.transaction(STORE, "readwrite");
    tx.objectStore(STORE).clear();
    tx.oncomplete = resolve;
    tx.onerror = () => reject(tx.error);
  });
}
