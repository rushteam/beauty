/*
 * botguard 浏览器 SDK v1（与 github.com/rushteam/beauty/pkg/middleware/botguard 配套）。
 *
 * 用法：
 *   await BotGuard.init({ tokenURL: '/_botguard/token' });
 *   const res = await BotGuard.fetch('/api/items?page=1', { method: 'POST', body: JSON.stringify(data) });
 *   // axios 等：const h = await BotGuard.headers('POST', url, body); 合并进请求头
 *
 * 需要安全上下文（HTTPS 或 localhost）以使用 WebCrypto。
 * body 仅支持 string / URLSearchParams / ArrayBuffer / TypedArray（需逐字节参与摘要）。
 * 生产发布前请对本文件做混淆 / JSVMP 虚拟化处理。
 *
 * 魔改算法（与服务端 WithCipher 使用同一个 id，seal 的输出即服务端 Open 的输入）：
 *   BotGuard.registerCipher({
 *     id: 'x1',
 *     seal: async function (ctx, plaintext, aad) { return new Uint8Array(...); },
 *   });
 *   await BotGuard.init({ cipher: 'x1', tokenURL: '/_botguard/token' });
 * ctx.raw 是会话密钥字节；ctx.cryptoKey 是同一密钥的 AES-GCM CryptoKey，仅默认算法使用。
 */
(function (global) {
  'use strict';

  var enc = new TextEncoder();
  var cryptoObj = global.crypto;
  var subtle = cryptoObj && cryptoObj.subtle;
  var origFetch = global.fetch.bind(global);
  var start = Date.now();

  var cfg = {
    tokenURL: '/_botguard/token',
    tokenHeader: 'X-Guard-Token',
    signHeader: 'X-Guard-Sign',
    powHeader: 'X-Guard-PoW',
    credentials: 'same-origin',
    cipher: 'v1'
  };
  var state = { token: '', key: null, keyRaw: null, exp: 0, skew: 0, fp: '', flags: 0, pending: null, ready: null };
  var ciphers = {
    v1: {
      id: 'v1',
      seal: async function (ctx, plain, aad) {
        var iv = cryptoObj.getRandomValues(new Uint8Array(12));
        var ct = new Uint8Array(await subtle.encrypt(
          { name: 'AES-GCM', iv: iv, additionalData: aad, tagLength: 128 },
          ctx.cryptoKey, plain));
        var out = new Uint8Array(12 + ct.length);
        out.set(iv); out.set(ct, 12);
        return out;
      }
    }
  };

  function registerCipher(c) {
    if (!c || typeof c.id !== 'string' || !/^[A-Za-z0-9]{1,16}$/.test(c.id) || typeof c.seal !== 'function') {
      throw new TypeError('botguard: cipher needs id /^[A-Za-z0-9]{1,16}$/ and seal(ctx, plaintext, aad)');
    }
    ciphers[c.id] = c;
  }

  // ---------- 行为采集 ----------
  var bhv = { mv: 0, cl: 0, kd: 0, sc: 0, tc: 0 };
  var pts = [];
  function listen(type, fn) {
    if (global.addEventListener) global.addEventListener(type, fn, { passive: true, capture: true });
  }
  listen('mousemove', function (e) {
    bhv.mv++;
    pts.push([e.clientX, e.clientY, e.timeStamp]);
    if (pts.length > 64) pts.shift();
  });
  listen('mousedown', function () { bhv.cl++; });
  listen('keydown', function () { bhv.kd++; });
  listen('scroll', function () { bhv.sc++; });
  listen('touchstart', function () { bhv.tc++; });

  function behavior() {
    var path = 0, speeds = [], st = 0, vc = 0, i;
    for (i = 1; i < pts.length; i++) {
      var d = Math.hypot(pts[i][0] - pts[i - 1][0], pts[i][1] - pts[i - 1][1]);
      var dt = pts[i][2] - pts[i - 1][2];
      path += d;
      if (dt > 0) speeds.push(d / dt);
    }
    if (pts.length > 2 && path > 0) {
      var a = pts[0], b = pts[pts.length - 1];
      st = Math.round(Math.hypot(b[0] - a[0], b[1] - a[1]) / path * 1000);
    }
    if (speeds.length > 2) {
      var mean = 0, sd = 0;
      for (i = 0; i < speeds.length; i++) mean += speeds[i];
      mean /= speeds.length;
      for (i = 0; i < speeds.length; i++) sd += (speeds[i] - mean) * (speeds[i] - mean);
      sd = Math.sqrt(sd / speeds.length);
      vc = mean > 0 ? Math.round(sd / mean * 1000) : 0;
    }
    return { mv: bhv.mv, cl: bhv.cl, kd: bhv.kd, sc: bhv.sc, tc: bhv.tc, dw: Date.now() - start, st: st, vc: vc };
  }

  // ---------- 指纹与环境检测 ----------
  function canvasFP() {
    try {
      var c = document.createElement('canvas');
      c.width = 240; c.height = 60;
      var x = c.getContext('2d');
      if (!x) return '';
      x.textBaseline = 'top';
      x.font = '16px Arial';
      x.fillStyle = '#f60'; x.fillRect(100, 1, 62, 20);
      x.fillStyle = '#069'; x.fillText('BotGuard,\ud83d\ude03 fp', 2, 15);
      x.fillStyle = 'rgba(102,204,0,0.7)'; x.fillText('BotGuard,\ud83d\ude03 fp', 4, 17);
      return c.toDataURL();
    } catch (e) { return ''; }
  }

  function webglFP() {
    try {
      var gl = document.createElement('canvas').getContext('webgl');
      if (!gl) return '';
      var ext = gl.getExtension('WEBGL_debug_renderer_info');
      return ext
        ? gl.getParameter(ext.UNMASKED_VENDOR_WEBGL) + '~' + gl.getParameter(ext.UNMASKED_RENDERER_WEBGL)
        : gl.getParameter(gl.VENDOR) + '~' + gl.getParameter(gl.RENDERER);
    } catch (e) { return ''; }
  }

  function audioFP() {
    return new Promise(function (resolve) {
      try {
        var A = global.OfflineAudioContext || global.webkitOfflineAudioContext;
        if (!A) return resolve('');
        var ctx = new A(1, 5000, 44100);
        var o = ctx.createOscillator();
        o.type = 'triangle'; o.frequency.value = 10000;
        var comp = ctx.createDynamicsCompressor();
        comp.threshold.value = -50; comp.knee.value = 40; comp.ratio.value = 12;
        comp.attack.value = 0; comp.release.value = 0.25;
        o.connect(comp); comp.connect(ctx.destination); o.start(0);
        var timer = setTimeout(function () { resolve(''); }, 1000);
        ctx.oncomplete = function (e) {
          clearTimeout(timer);
          var d = e.renderedBuffer.getChannelData(0), s = 0;
          for (var i = 4500; i < 5000; i++) s += Math.abs(d[i]);
          resolve(s.toString());
        };
        ctx.startRendering();
      } catch (e) { resolve(''); }
    });
  }

  function isNative(fn) {
    try {
      return typeof fn === 'function' && /\{\s*\[native code\]\s*\}\s*$/.test(Function.prototype.toString.call(fn));
    } catch (e) { return false; }
  }

  function envFlags(gl, cv) {
    var n = global.navigator || {}, ua = n.userAgent || '', f = 0;
    if (n.webdriver) f |= 1;
    if (/HeadlessChrome/.test(ua)) f |= 2;
    if (/Chrome\//.test(ua) && !global.chrome) f |= 4;
    try {
      var d = global.Navigator && Object.getOwnPropertyDescriptor(global.Navigator.prototype, 'webdriver');
      if (!isNative(Function.prototype.toString) ||
          (global.HTMLCanvasElement && !isNative(global.HTMLCanvasElement.prototype.toDataURL)) ||
          (d && d.get && !isNative(d.get)) ||
          Object.getOwnPropertyNames(n).length > 0) f |= 8;
    } catch (e) { f |= 8; }
    if (global.outerWidth === 0 || global.outerHeight === 0) f |= 16;
    if (!n.plugins || n.plugins.length === 0) f |= 32;
    if (!n.languages || n.languages.length === 0) f |= 64;
    if (!cv) f |= 128;
    if (/SwiftShader|llvmpipe|softpipe|Software/i.test(gl)) f |= 256;
    return f;
  }

  async function collect() {
    var n = global.navigator || {}, s = global.screen || {};
    var cv = canvasFP(), gl = webglFP(), au = await audioFP(), tz = '', plugins = [];
    try { tz = Intl.DateTimeFormat().resolvedOptions().timeZone; } catch (e) { /* ignore */ }
    try { for (var i = 0; i < n.plugins.length; i++) plugins.push(n.plugins[i].name); } catch (e) { /* ignore */ }
    var parts = [
      n.userAgent, (n.languages || []).join(','), n.platform, n.hardwareConcurrency, n.deviceMemory,
      [s.width, s.height, s.colorDepth].join('x'), tz, new Date().getTimezoneOffset(),
      n.maxTouchPoints, plugins.join(','), hex(await sha256(enc.encode(cv))), gl, au
    ];
    state.flags = envFlags(gl, cv);
    state.fp = hex(await sha256(enc.encode(parts.join('|'))));
  }

  // ---------- 编码与哈希 ----------
  function hex(buf) {
    var b = new Uint8Array(buf), s = '';
    for (var i = 0; i < b.length; i++) s += (b[i] < 16 ? '0' : '') + b[i].toString(16);
    return s;
  }
  function sha256(bytes) { return subtle.digest('SHA-256', bytes); }
  function b64u(b) {
    var s = '';
    for (var i = 0; i < b.length; i++) s += String.fromCharCode(b[i]);
    return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  }
  function unb64u(s) {
    s = s.replace(/-/g, '+').replace(/_/g, '/');
    while (s.length % 4) s += '=';
    var r = atob(s), b = new Uint8Array(r.length);
    for (var i = 0; i < r.length; i++) b[i] = r.charCodeAt(i);
    return b;
  }
  function nonce() {
    return hex(cryptoObj.getRandomValues(new Uint8Array(16)));
  }

  // 同步 SHA-256（仅用于 PoW 大量短消息哈希，避免 WebCrypto 异步调用开销）
  var K = new Uint32Array([
    0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
    0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
    0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
    0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
    0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
    0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
    0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
    0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2
  ]);
  var W = new Uint32Array(64);
  function sha256Sync(m) {
    var l = m.length, n = ((l + 9 + 63) >> 6) << 6, p = new Uint8Array(n), i;
    p.set(m); p[l] = 0x80;
    var bitLen = l * 8;
    p[n - 4] = bitLen >>> 24; p[n - 3] = (bitLen >>> 16) & 255; p[n - 2] = (bitLen >>> 8) & 255; p[n - 1] = bitLen & 255;
    var H = [0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19];
    for (var o = 0; o < n; o += 64) {
      for (i = 0; i < 16; i++) W[i] = (p[o + 4 * i] << 24) | (p[o + 4 * i + 1] << 16) | (p[o + 4 * i + 2] << 8) | p[o + 4 * i + 3];
      for (i = 16; i < 64; i++) {
        var x = W[i - 15], y = W[i - 2];
        var s0 = ((x >>> 7) | (x << 25)) ^ ((x >>> 18) | (x << 14)) ^ (x >>> 3);
        var s1 = ((y >>> 17) | (y << 15)) ^ ((y >>> 19) | (y << 13)) ^ (y >>> 10);
        W[i] = (W[i - 16] + s0 + W[i - 7] + s1) | 0;
      }
      var a = H[0], b = H[1], c = H[2], d = H[3], e = H[4], f = H[5], g = H[6], h = H[7];
      for (i = 0; i < 64; i++) {
        var S1 = ((e >>> 6) | (e << 26)) ^ ((e >>> 11) | (e << 21)) ^ ((e >>> 25) | (e << 7));
        var t1 = (h + S1 + ((e & f) ^ (~e & g)) + K[i] + W[i]) | 0;
        var S0 = ((a >>> 2) | (a << 30)) ^ ((a >>> 13) | (a << 19)) ^ ((a >>> 22) | (a << 10));
        var t2 = (S0 + ((a & b) ^ (a & c) ^ (b & c))) | 0;
        h = g; g = f; f = e; e = (d + t1) | 0; d = c; c = b; b = a; a = (t1 + t2) | 0;
      }
      H[0] = (H[0] + a) | 0; H[1] = (H[1] + b) | 0; H[2] = (H[2] + c) | 0; H[3] = (H[3] + d) | 0;
      H[4] = (H[4] + e) | 0; H[5] = (H[5] + f) | 0; H[6] = (H[6] + g) | 0; H[7] = (H[7] + h) | 0;
    }
    return H;
  }
  function zeroBits(H) {
    for (var i = 0, z = 0; i < 8; i++) {
      var w = H[i] >>> 0;
      if (w !== 0) return z + Math.clz32(w);
      z += 32;
    }
    return z;
  }

  // 分片求解，每批之间让出主线程，避免低端机卡顿
  function solve(pow) {
    var prefix = pow.challenge + ':', n = 0;
    return new Promise(function (resolve) {
      (function batch() {
        for (var end = n + 5000; n < end; n++) {
          var h = prefix + n;
          if (zeroBits(sha256Sync(enc.encode(h))) >= pow.bits) return resolve(h);
        }
        setTimeout(batch, 0);
      })();
    });
  }

  // ---------- 请求摘要与签名 ----------
  function baseURL() { return global.location ? global.location.href : undefined; }

  async function digest(method, url, bodyBytes) {
    var u = new URL(url, baseURL()), path = u.pathname, pairs = [];
    try { path = decodeURIComponent(path); } catch (e) { /* keep raw */ }
    u.searchParams.forEach(function (v, k) { pairs.push(encodeURIComponent(k) + '=' + encodeURIComponent(v)); });
    pairs.sort();
    var bh = hex(await sha256(bodyBytes));
    return sha256(enc.encode(method.toUpperCase() + '\n' + path + '\n' + pairs.join('&') + '\n' + bh));
  }

  function toBytes(body) {
    if (body == null) return new Uint8Array(0);
    if (typeof body === 'string') return enc.encode(body);
    if (typeof URLSearchParams !== 'undefined' && body instanceof URLSearchParams) return enc.encode(body.toString());
    if (body instanceof ArrayBuffer) return new Uint8Array(body);
    if (ArrayBuffer.isView(body)) return new Uint8Array(body.buffer, body.byteOffset, body.byteLength);
    throw new TypeError('botguard: unsupported body type, use string / URLSearchParams / ArrayBuffer');
  }

  function now() { return Date.now() + state.skew; }

  function refresh() {
    if (state.pending) return state.pending;
    state.pending = (async function () {
      var t0 = Date.now();
      var res = await origFetch(cfg.tokenURL, {
        method: 'POST',
        credentials: cfg.credentials,
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ fp: state.fp })
      });
      if (!res.ok) throw new Error('botguard: token request failed ' + res.status);
      var j = await res.json();
      state.skew = j.now - Math.round((t0 + Date.now()) / 2);
      state.keyRaw = unb64u(j.key);
      state.key = await subtle.importKey('raw', state.keyRaw, { name: 'AES-GCM' }, false, ['encrypt']);
      state.token = j.token;
      state.exp = j.exp * 1000;
    })().finally(function () { state.pending = null; });
    return state.pending;
  }

  async function ensureToken() {
    await init();
    if (state.pending) await state.pending;
    if (!state.token || now() > state.exp - 10000) await refresh();
  }

  async function sign(method, url, bodyBytes) {
    var c = ciphers[cfg.cipher];
    if (!c) throw new Error('botguard: unknown cipher ' + cfg.cipher);
    var aad = new Uint8Array(await digest(method, url, bodyBytes));
    var payload = { ts: now(), n: nonce(), fp: state.fp, f: state.flags, b: behavior() };
    var sealed = await c.seal(
      { raw: state.keyRaw, cryptoKey: state.key },
      enc.encode(JSON.stringify(payload)),
      aad);
    return c.id + '.' + b64u(sealed instanceof Uint8Array ? sealed : new Uint8Array(sealed));
  }

  // headers 为非 fetch 客户端（axios / XHR）生成风控头。每次请求都必须重新生成（nonce 一次性）。
  async function headers(method, url, body) {
    await ensureToken();
    var h = {};
    h[cfg.tokenHeader] = state.token;
    h[cfg.signHeader] = await sign(method || 'GET', url, toBytes(body));
    return h;
  }

  async function jsonOf(res) {
    try { return await res.clone().json(); } catch (e) { return null; }
  }

  async function guardFetch(input, opts) {
    opts = opts || {};
    var url = typeof input === 'string' ? input : input.href || input.url;
    var method = (opts.method || 'GET').toUpperCase();
    var bytes = toBytes(opts.body), pow = '', res;
    for (var attempt = 0; attempt < 3; attempt++) {
      await ensureToken();
      var h = new Headers(opts.headers || {});
      h.set(cfg.tokenHeader, state.token);
      h.set(cfg.signHeader, await sign(method, url, bytes));
      if (pow) h.set(cfg.powHeader, pow);
      res = await origFetch(url, Object.assign({}, opts, { method: method, headers: h }));
      if (res.headers.get('X-Guard-Refresh')) refresh().catch(function () { /* 下次请求再试 */ });
      if (res.status === 401) {
        var j = await jsonOf(res);
        if (j && j.code === 'token_required') { state.token = ''; continue; }
      } else if (res.status === 428) {
        var c = await jsonOf(res);
        if (c && c.pow && !pow) { pow = await solve(c.pow); continue; }
      }
      return res;
    }
    return res;
  }

  function init(opts) {
    if (opts) for (var k in opts) if (Object.prototype.hasOwnProperty.call(opts, k)) cfg[k] = opts[k];
    if (!state.ready) {
      if (!subtle) return Promise.reject(new Error('botguard: WebCrypto unavailable (需要 HTTPS 或 localhost)'));
      state.ready = collect().then(refresh);
      state.ready.catch(function () { state.ready = null; });
    }
    return state.ready;
  }

  global.BotGuard = { init: init, fetch: guardFetch, headers: headers, solve: solve, registerCipher: registerCipher };
})(typeof window !== 'undefined' ? window : globalThis);
