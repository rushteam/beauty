# contrib/jsvm —— JavaScript 扩展层(独立模块)

用 **goja**(纯 Go、零 CGo 的 JS 引擎)以 PocketBase JSVM 的风格把 beauty 核心能力暴露给脚本:
路由、定时任务、钩子、事件总线、HTTP 客户端、安全原语、对象存储。业务扩展写成 `.js` 文件即可,
不用重新编译 Go。

```bash
go get github.com/rushteam/beauty/contrib/jsvm@latest
```

## 接入

```go
bucket, _ := blob.NewLocal("./data")
bus := eventbus.New[any]()

vm, err := jsvm.New(
    jsvm.WithDir("hooks"),                 // 加载 hooks/*.js(按文件名排序),同时作为 require 的模块根
    jsvm.WithBucket(bucket),               // 启用 $filesystem
    jsvm.WithEventBus(bus),                // 启用 $app.publish / eventSubscribe
    jsvm.WithTimeout(5*time.Second),       // 单次 JS 调用超时即中断(死循环也能打断)
    jsvm.WithWatch(true),                  // hooks/ 下的 .js/.json 变化时自动重载
    jsvm.WithGlobal("orders", orderSvc),   // 暴露自定义 Go 能力:orders.create(...)
)
if err != nil { log.Fatal(err) }

app := beauty.New(
    beauty.WithService(vm),                // 运行 onStart/onStop 与脚本里的 cron
    beauty.WithWebServer(":8080", vm),     // vm 实现 http.Handler;也可挂到已有 mux 的子路径下
)
_ = app.Start(ctx)

// Go 侧定义扩展点,脚本用 hookAdd 订阅;脚本对 event 的修改回到 Go 可见,throw 即中止。
ev := map[string]any{"amount": 100}
if err := vm.Trigger(ctx, "order.create", ev); err != nil { /* *jsvm.Error,Status 来自 ApiError */ }
```

## 脚本 API

```js
// hooks/pricing.js
module.exports = {
  discount(amount) { return amount > 1000 ? 0.9 : 1; },
};

// hooks/main.js
const pricing = require("./pricing");

routerUse((e) => { e.setHeader("X-Powered-By", "beauty"); return e.next(); });

const auth = (e) => {
  const token = e.request.header("Authorization").replace("Bearer ", "");
  let claims;
  try { claims = $security.parseJWT(token, "secret"); } catch (_) { throw new UnauthorizedError(); }
  e.set("uid", claims.sub);
  return e.next();
};

routerAdd("POST", "/files/{key...}", (e) => {
  $filesystem.put(e.request.pathValue("key"), e.request.text(), { metadata: { by: e.get("uid") } });
  e.json(201, { ok: true });
}, auth);

routerAdd("GET", "/weather/{city}", (e) => {
  const res = $http.send({ url: "https://api.example.com/w?c=" + e.request.pathValue("city"), timeout: 3 });
  if (res.statusCode !== 200) throw new ApiError(502, "upstream failed");
  e.json(200, res.json);
});

hookAdd("order.create", (e) => {
  if (e.amount <= 0) throw new BadRequestError("amount must be positive");
  e.discount = pricing.discount(e.amount);
});

cronAdd("cleanup", "0 */5 * * * *", () => {           // 秒级 spec,同 pkg/service/cron
  $app.logger().info("cleanup", "removed", $filesystem.list("tmp/").length);
});

eventSubscribe("user.signup", (topic, user) => $app.publish("mail.welcome", { to: user.email }));
onStart(() => console.log("jsvm ready"));
```

| 对象 / 函数 | 说明 |
|---|---|
| `routerAdd(method, path, handler, ...mw)` | Go 1.22 ServeMux 模式(`{id}`、`{path...}`),method 为 `"*"` 表示任意方法 |
| `routerUse(mw)` | 作用于脚本所有路由的中间件,调用 `e.next()` 继续 |
| `e.request` | `method/url/path/host/remoteAddr/proto`,`header()/headers()/query()/pathValue()/cookie()/formValue()/text()/json()` |
| `e.json/string/html/blob/noContent/redirect/setHeader/set/get/next` | 写响应、请求内传值、继续链路 |
| `require(spec)` | 加载模块。`./`、`../` 相对当前文件;`pricing` 依次找 `pricing.js`、`pricing.json`、`pricing/index.js`。模块用 `module.exports` / `exports` 导出,每个实例缓存一份 |
| `cronAdd(name, spec, fn)` / `cronRemove(name)` | 定时任务;未 `WithCron` 时 VM 自建 Cron 并在 `Start` 中运行 |
| `hookAdd(name, fn)` / `onStart` / `onStop` | Go 侧 `vm.Trigger` 触发;脚本内可 `$app.trigger(name, event)` |
| `eventSubscribe(topic, fn)` | 订阅 `pkg/messaging/eventbus`,JS 订阅者总是异步执行 |
| `$app` | `logger()`、`store()`(跨实例共享 KV)、`trigger()`、`publish()` |
| `$http.send({...})` | 继承调用 ctx;返回 `statusCode/headers/raw/json`,非 2xx 不抛异常 |
| `$security` | `randomString`、`md5/sha256/sha512/hs256/hs512`、`equal`、`encrypt/decrypt`(AES-GCM)、`hashPassword/checkPassword`(bcrypt)、`createJWT/parseJWT/parseUnverifiedJWT` |
| `$filesystem` | `put/get/getString/stat/exists/delete/copy/list`,对接任意 `blob.Bucket` |
| `$os` | `getenv/readFile/writeFile/readDir/mkdirAll/remove/removeAll/exec`,**默认关闭**,需 `WithOS(true)` |
| `ApiError(status, msg, data)` 及 `BadRequestError/UnauthorizedError/ForbiddenError/NotFoundError` | 路由中抛出时回写 `{code, message, data}`;其他异常一律 500 且不泄露细节 |
| `console.*`、`sleep(ms)` | 日志走 `WithLogger`(默认 slog);`sleep` 可被取消 |

## 模块

入口脚本(WithDir / WithFS / WithScript 加载的那些)按文件名顺序在同一个全局作用域里执行,
`var` 和函数声明跨文件可见。被 `require` 的文件是模块:独立作用域,只通过 `module.exports` 对外。

```js
// hooks/lib/tax.js
exports.rate = 0.06;

// hooks/main.js
const tax = require("./lib/tax");   // 相对当前文件
const util = require("util");       // 裸路径:按模块根查找 util.js / util.json / util/index.js
```

- 模块根按注册顺序查找:每个 WithDir、WithFS,然后是 WithModuleFS(只供 require,不自动执行)。
- 循环依赖拿到的是对方已经赋值的那部分 `exports`。
- 相对路径不能逃出模块根(`require("../../etc/passwd")` 会抛错);内联脚本没有文件位置,不能用相对路径。
- 入口脚本里可以用 `__filename` 和 `__dirname`。

## 热重载

`vm.Reload(ctx)` 重新读取并编译全部入口脚本,成功后整体切换:

- 正在处理的请求、正在跑的定时任务和事件回调用旧脚本跑完;
- 之后的请求、`vm.Trigger` 和新的定时任务用新脚本;
- `$app.store()` 的内容保留;
- 编译失败时返回错误,继续服务旧脚本;
- 切换成功后触发 `onReload`。

`WithWatch(true)` 在 `VM.Start` 期间递归监听 WithDir,`.js` / `.json` 变化后自动 `Reload`
(默认去抖 200ms,用 `WithWatchDelay` 调整)。embed.FS 和内联脚本不会变化,需要主动调用 `Reload`。

## 并发模型与限制

- goja 实例不可并发,VM 维护实例池(`WithPoolSize`,默认 GOMAXPROCS)。**每个实例完整执行一遍脚本**,
  注册函数只在首个实例真正挂到路由 / cron / 钩子;调用时借出任意实例执行它自己那份同序号的函数。
  - 处理函数可以正常使用闭包;但**顶层变量每个实例一份**,跨请求共享状态用 `$app.store()`。
  - 注册函数只能在脚本顶层调用,且各实例的注册序列必须一致(不要按随机数/时间条件注册),否则 `New` 报错。
  - 顶层代码执行"池大小"次,只做注册,避免副作用(非首个实例加载期的日志会被丢弃)。
- `WithTimeout` 同时限制脚本加载与每次调用;HTTP 处理还受请求 ctx 约束。不设超时时,cron/钩子里的死循环会一直占住实例。
- Go 绑定(含 `WithGlobal` 暴露的函数)发生 panic 时,本次调用返回错误,该实例被丢弃并按脚本重建。
- 不要在 JS 调用链内(如 `WithGlobal` 暴露的 Go 函数中)调用 `vm.Trigger`:池被占满时会等到 ctx 结束;脚本内请用 `$app.trigger`。
- 暂不支持 Promise/async 事件循环,也不支持 `import` / `export` 语法(用 `require` 与 `module.exports`)。

## 与 contrib/wasm 的取舍

| | jsvm | wasm |
|---|---|---|
| 语言 | JavaScript | 任意可编译到 wasm 的语言 |
| 能力边界 | 通过 Option 授予网络、存储、命令执行 | 默认纯计算,只能调显式授予的 host function |
| 适合 | 业务团队快速写路由 / 钩子 / 定时任务 | 不受信任代码、策略引擎、跨语言插件 |
