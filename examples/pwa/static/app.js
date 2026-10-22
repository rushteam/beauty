function log(msg) {
  const el = document.getElementById("log");
  el.textContent = msg + "\n" + el.textContent;
}

async function registerSW() {
  if (!("serviceWorker" in navigator)) {
    log("Service Worker 不支持");
    return null;
  }
  const reg = await navigator.serviceWorker.register("/sw.js");
  log("SW registered: " + reg.scope);
  return reg;
}

function urlBase64ToUint8Array(base64String) {
  const padding = "=".repeat((4 - (base64String.length % 4)) % 4);
  const base64 = (base64String + padding).replace(/-/g, "+").replace(/_/g, "/");
  const raw = atob(base64);
  const out = new Uint8Array(raw.length);
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
  return out;
}

async function enablePush(reg) {
  const { publicKey } = await fetch("/push/vapid").then((r) => r.json());
  const sub = await reg.pushManager.subscribe({
    userVisibleOnly: true,
    applicationServerKey: urlBase64ToUint8Array(publicKey),
  });
  await fetch("/push/subscribe?user=demo", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(sub),
  });
  log("Push subscribed");
}

async function fetchPublic() {
  const res = await fetch("/api/public/feed");
  const text = await res.text();
  log("GET /api/public/feed → " + res.status + " Cache-Control=" + res.headers.get("Cache-Control") + " " + text);
}

async function placeOrder() {
  const key = crypto.randomUUID();
  const res = await fetch("/api/orders", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "Idempotency-Key": key,
    },
    body: JSON.stringify({ sku: "tea", qty: 1 }),
  });
  const text = await res.text();
  log("POST /api/orders key=" + key + " → " + res.status + " replayed=" + res.headers.get("X-Idempotency-Replayed") + " " + text);
}

async function sendPush() {
  const res = await fetch("/push/send?user=demo", { method: "POST" });
  log("POST /push/send → " + res.status + " " + (await res.text()));
}

async function flushSync() {
  if (navigator.serviceWorker.controller) {
    // 触发 SW 内 flush（若浏览器不支持 Background Sync，手动调 /sync）
  }
  const res = await fetch("/sync", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ ops: [] }),
  }).catch(() => null);
  log("manual sync ping (empty ops may 400): " + (res && res.status));
}

window.addEventListener("load", async () => {
  const reg = await registerSW();
  document.getElementById("btn-feed").onclick = fetchPublic;
  document.getElementById("btn-order").onclick = placeOrder;
  document.getElementById("btn-push").onclick = async () => {
    if (reg) await enablePush(reg);
  };
  document.getElementById("btn-send").onclick = sendPush;
  document.getElementById("btn-sync").onclick = flushSync;
});
