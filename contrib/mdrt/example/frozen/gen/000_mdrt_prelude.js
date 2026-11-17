// mdrt prelude。由运行时注入,不由模型生成。
// store 索引用 JSON 字符串保存:跨 goja 来回 Export 后数组不一定还能 push。
var __mdrt = {
  keysKey: "__mdrt:keys",

  remember: function (full) {
    var store = $app.store();
    var keys = [];
    var raw = store.get(this.keysKey);
    if (typeof raw === "string" && raw !== "") {
      try { keys = JSON.parse(raw); } catch (e) { keys = []; }
    }
    for (var i = 0; i < keys.length; i++) {
      if (keys[i] === full) return;
    }
    keys.push(full);
    store.set(this.keysKey, JSON.stringify(keys));
  },

  reset: function () {
    var store = $app.store();
    var raw = store.get(this.keysKey);
    var keys = [];
    if (typeof raw === "string" && raw !== "") {
      try { keys = JSON.parse(raw); } catch (e) { keys = []; }
    }
    for (var i = 0; i < keys.length; i++) store.remove(keys[i]);
    store.remove(this.keysKey);
  },

  hostOf: function (url) {
    var m = /^https?:\/\/([^\/:?#]+)/i.exec(String(url || ""));
    if (!m) throw new BadRequestError("mdrt: bad url");
    return String(m[1]).toLowerCase();
  },

  ctx: function (slug, idx, uid, allow) {
    var self = this;
    return {
      uid: uid || "",
      store: {
        get: function (k) { return $app.store().get(slug + ":" + k); },
        set: function (k, v) {
          var full = slug + ":" + k;
          $app.store().set(full, v);
          self.remember(full);
        },
        has: function (k) { return $app.store().has(slug + ":" + k); },
        remove: function (k) {
          var full = slug + ":" + k;
          $app.store().remove(full);
        }
      },
      http: {
        send: function (req) {
          var host = self.hostOf(req && req.url);
          var list = allow || [];
          var ok = false;
          for (var i = 0; i < list.length; i++) {
            if (String(list[i]).toLowerCase() === host) ok = true;
          }
          if (!ok) throw new ForbiddenError("mdrt: host not allowed: " + host);
          return $http.send(req);
        }
      },
      log: {
        info: function (m) { $app.logger().info(String(m), "spec", slug, "unit", idx); },
        warn: function (m) { $app.logger().warn(String(m), "spec", slug, "unit", idx); },
        error: function (m) { $app.logger().error(String(m), "spec", slug, "unit", idx); }
      },
      now: function () {
        if (typeof __mdrtConf !== "undefined" && __mdrtConf.nowMs > 0) return __mdrtConf.nowMs;
        return Date.now();
      },
      id: function () { return $security.randomString(16); },
      fail: function (status, msg, data) { throw new ApiError(status || 500, msg || "", data); },
      trigger: function (name, event) { return $app.trigger(name, event || {}); }
    };
  },

  bearer: function (e) {
    if (!e) return "";
    var h = e.request.header("Authorization") || "";
    var m = /^Bearer\s+(.+)$/.exec(h);
    if (!m) return "";
    try {
      var secret = (typeof __mdrtConf !== "undefined" && __mdrtConf.jwtSecret) || "";
      var c = $security.parseJWT(m[1], secret);
      return String((c && c.sub) || "");
    } catch (err) {
      return "";
    }
  },

  read: function (schema, rawOf) {
    var names = Object.keys(schema || {});
    var out = {};
    var errors = [];
    for (var i = 0; i < names.length; i++) {
      var name = names[i];
      var f = schema[name];
      var raw = rawOf(name, f);
      var missing = raw === undefined || raw === null || (f.in !== "body" && String(raw) === "");
      if (missing) {
        if (!f.optional) errors.push({ field: name, message: "required" });
        continue;
      }
      var v = this.coerce(f, raw, errors, name);
      if (v !== undefined) out[name] = v;
    }
    if (errors.length) throw new BadRequestError("invalid input", { fields: errors });
    return out;
  },

  coerce: function (f, raw, errors, name) {
    var v = raw;
    if (f.in !== "body") {
      if (f.type === "number" || f.type === "integer") {
        if (typeof raw !== "number") {
          if (!/^-?\d+(\.\d+)?$/.test(String(raw))) {
            errors.push({ field: name, message: "not a number" });
            return;
          }
          v = Number(raw);
        }
      } else if (f.type === "boolean") {
        var s = String(raw);
        if (s === "true" || s === "1") v = true;
        else if (s === "false" || s === "0") v = false;
        else {
          errors.push({ field: name, message: "not a boolean" });
          return;
        }
      } else if (f.type === "string") {
        v = String(raw);
      }
    }
    if (f.type === "integer" && typeof v === "number" && Math.floor(v) !== v) {
      errors.push({ field: name, message: "not an integer" });
      return;
    }
    if ((f.type === "number" || f.type === "integer") && typeof v === "number") {
      if (f.gt !== undefined && !(v > f.gt)) { errors.push({ field: name, message: "gt" }); return; }
      if (f.gte !== undefined && !(v >= f.gte)) { errors.push({ field: name, message: "gte" }); return; }
      if (f.lt !== undefined && !(v < f.lt)) { errors.push({ field: name, message: "lt" }); return; }
      if (f.lte !== undefined && !(v <= f.lte)) { errors.push({ field: name, message: "lte" }); return; }
    }
    if (f.type === "string") {
      v = String(v);
      if (f.minLen !== undefined && v.length < f.minLen) { errors.push({ field: name, message: "minLen" }); return; }
      if (f.maxLen !== undefined && v.length > f.maxLen) { errors.push({ field: name, message: "maxLen" }); return; }
      if (f.pattern && !(new RegExp(f.pattern)).test(v)) { errors.push({ field: name, message: "pattern" }); return; }
    }
    if (f.enum) {
      var ok = false;
      var sv = String(v);
      for (var i = 0; i < f.enum.length; i++) {
        if (f.enum[i] === v || String(f.enum[i]) === sv) ok = true;
      }
      if (!ok) { errors.push({ field: name, message: "enum" }); return; }
    }
    return v;
  },

  input: function (e, schema) {
    var body;
    var loaded = false;
    return this.read(schema, function (name, f) {
      if (f.in === "path") return e.request.pathValue(name);
      if (f.in === "query") return e.request.query(name);
      if (f.in === "header") return e.request.header(name);
      if (!loaded) {
        body = e.request.json();
        loaded = true;
      }
      if (!body) return undefined;
      return body[name];
    });
  },

  pick: function (obj, schema) {
    return this.read(schema, function (name) {
      return obj ? obj[name] : undefined;
    });
  },

  respond: function (e, result) {
    if (result === undefined || result === null) {
      e.noContent(204);
      return;
    }
    var status = result.status || 200;
    if (result.headers) {
      var hk = Object.keys(result.headers);
      for (var i = 0; i < hk.length; i++) e.setHeader(hk[i], String(result.headers[hk[i]]));
    }
    if (result.body === undefined) {
      e.noContent(status);
      return;
    }
    if (typeof result.body === "string") {
      e.string(status, result.body);
      return;
    }
    e.json(status, result.body);
  },

  invoke: function (kind, ev, fn, schema, slug, idx, allow) {
    var ctx = this.ctx(slug, idx, (ev && ev.uid) || "", allow);
    if (kind === "cron") {
      fn(ctx);
      return;
    }
    var obj = JSON.parse((ev && ev.json) || "{}");
    var input = this.pick(obj, schema);
    if (kind === "hook") fn(obj, input, ctx);
    else fn(obj, input, ctx);
    ev.out = JSON.stringify(obj);
  }
};

hookAdd("mdrt:reset", function () { __mdrt.reset(); });

hookAdd("mdrt:seed", function (ev) {
  __mdrt.reset();
  var data = JSON.parse((ev && ev.json) || "{}");
  var keys = Object.keys(data);
  for (var i = 0; i < keys.length; i++) {
    $app.store().set(keys[i], data[keys[i]]);
    __mdrt.remember(keys[i]);
  }
});

hookAdd("mdrt:dump", function (ev) {
  var store = $app.store();
  var raw = store.get(__mdrt.keysKey);
  var keys = [];
  if (typeof raw === "string" && raw !== "") {
    try { keys = JSON.parse(raw); } catch (e) { keys = []; }
  }
  var out = {};
  for (var i = 0; i < keys.length; i++) {
    if (keys[i] === __mdrt.keysKey) continue;
    out[keys[i]] = store.get(keys[i]);
  }
  ev.json = JSON.stringify(out);
});
