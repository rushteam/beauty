package jsvm

import (
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/dop251/goja"

	"github.com/rushteam/beauty/pkg/messaging/eventbus"
	"github.com/rushteam/beauty/pkg/service/cron"
	"github.com/rushteam/beauty/pkg/store/blob"
)

// Option 配置 VM。
type Option func(*config)

type config struct {
	sources     []sourceSpec
	moduleFS    []fs.FS
	poolSize    int
	timeout     time.Duration
	stopTimeout time.Duration
	maxBodySize int64
	logger      *slog.Logger
	httpClient  *http.Client
	bucket      blob.Bucket
	bus         *eventbus.Bus[any]
	cron        *cron.Cron
	enableOS    bool
	watch       bool
	watchDelay  time.Duration
	globals     []global
	setups      []func(*goja.Runtime) error
}

// sourceSpec 是一段脚本或一组待展开的文件(fsys != nil)。按 Option 出现顺序加载。
type sourceSpec struct {
	name    string
	code    string
	fsys    fs.FS
	pattern string
	dir     string // 非空表示本地目录(WithDir):既是 require 的根,也是 WithWatch 的监听对象
	rootID  int    // indexRoots 填入
}

type global struct {
	name  string
	value any
}

func defaultConfig() config {
	return config{
		poolSize:    runtime.GOMAXPROCS(0),
		stopTimeout: 10 * time.Second,
		maxBodySize: 32 << 20,
		watchDelay:  200 * time.Millisecond,
	}
}

// WithDir 加载 dir 下(不递归)所有 *.js 文件,按文件名排序。
// 该目录同时成为 require 的模块根(子目录里的文件不会自动执行,但可以被 require),
// 并在 WithWatch 时被递归监听。
func WithDir(dir string) Option {
	return func(c *config) {
		c.sources = append(c.sources, sourceSpec{fsys: os.DirFS(dir), pattern: "*.js", dir: dir})
	}
}

// WithFS 加载 fsys 中匹配 pattern(fs.Glob 语法)的文件,按路径排序。适合配合 embed.FS。
// fsys 同时成为 require 的模块根,因此入口脚本可以 require 同 fs 里的其他文件。
func WithFS(fsys fs.FS, pattern string) Option {
	return func(c *config) { c.sources = append(c.sources, sourceSpec{fsys: fsys, pattern: pattern}) }
}

// WithModuleFS 额外注册一个 require 模块根,其中的文件不会自动执行。
// 适合放公共库:require("pricing") 会依次查找各模块根下的 pricing.js / pricing.json / pricing/index.js。
func WithModuleFS(fsys fs.FS) Option {
	return func(c *config) { c.moduleFS = append(c.moduleFS, fsys) }
}

// WithScript 加载一段内联脚本,name 用于报错定位。
func WithScript(name, code string) Option {
	return func(c *config) { c.sources = append(c.sources, sourceSpec{name: name, code: code}) }
}

// WithPoolSize 设置 VM 实例池大小(默认 GOMAXPROCS)。goja 实例不可并发,池大小即 JS 的最大并发度。
//
// 每个实例都会完整执行一遍脚本,所以脚本顶层应只做注册;顶层的副作用(写文件、发请求)
// 会执行 n 次。非首个实例在加载期的 console / logger 输出会被丢弃。
func WithPoolSize(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.poolSize = n
		}
	}
}

// WithTimeout 限制单次 JS 调用(以及每个实例的脚本加载)的最长执行时间,超时即中断。默认不限,
// 此时 HTTP 处理仍受请求 ctx 约束,但 cron/钩子里的死循环会一直占住实例。
func WithTimeout(d time.Duration) Option {
	return func(c *config) { c.timeout = d }
}

// WithStopTimeout 设置停机时 onStop 钩子的最长执行时间(默认 10s)。
func WithStopTimeout(d time.Duration) Option {
	return func(c *config) { c.stopTimeout = d }
}

// WithMaxBodySize 限制脚本读取的请求体与 $http.send 响应体大小(默认 32MB)。
func WithMaxBodySize(n int64) Option {
	return func(c *config) {
		if n > 0 {
			c.maxBodySize = n
		}
	}
}

// WithLogger 设置 console 与 $app.logger() 使用的 logger(默认 slog.Default())。
func WithLogger(l *slog.Logger) Option {
	return func(c *config) { c.logger = l }
}

// WithHTTPClient 设置 $http.send 使用的客户端(默认 pkg/client/http.NewHTTPClient(),带 OTel 传播)。
// 可传入带服务发现 / 重试 / 熔断的客户端。
func WithHTTPClient(hc *http.Client) Option {
	return func(c *config) { c.httpClient = hc }
}

// WithBucket 启用 $filesystem,读写给定的对象存储。
func WithBucket(b blob.Bucket) Option {
	return func(c *config) { c.bucket = b }
}

// WithEventBus 启用 $app.publish 与 eventSubscribe,与 Go 侧共享同一条事件总线。
func WithEventBus(bus *eventbus.Bus[any]) Option {
	return func(c *config) { c.bus = bus }
}

// WithCron 让 cronAdd 注册到给定的 Cron(需自行 beauty.WithService 运行它)。
// 不设置时,VM 在脚本用到 cronAdd 时自建一个 Cron,并在 VM.Start 中运行。
func WithCron(cr *cron.Cron) Option {
	return func(c *config) { c.cron = cr }
}

// WithOS 启用 $os(读写本地文件、环境变量、执行命令)。默认关闭:开启即意味着脚本拥有进程同等权限。
func WithOS(enable bool) Option {
	return func(c *config) { c.enableOS = enable }
}

// WithGlobal 向每个实例注入一个全局变量,是把业务 Go 能力暴露给脚本的主要扩展点。
// Go 结构体的字段/方法按首字母小写映射(Foo → foo)。value 会被所有实例共享,须并发安全。
func WithGlobal(name string, value any) Option {
	return func(c *config) { c.globals = append(c.globals, global{name: name, value: value}) }
}

// WithSetup 在每个实例加载脚本前执行 fn,用于需要直接操作 goja.Runtime 的高级定制。
func WithSetup(fn func(rt *goja.Runtime) error) Option {
	return func(c *config) { c.setups = append(c.setups, fn) }
}

// WithWatch 在 VM.Start 期间监听 WithDir 目录(递归,含模块子目录),*.js / *.json 变化时自动 Reload。
// 只监听本地目录;embed.FS 与 WithScript 不会变化。监听失败或重载失败只记日志,继续服务旧脚本。
func WithWatch(enable bool) Option {
	return func(c *config) { c.watch = enable }
}

// WithWatchDelay 设置文件变更后触发 Reload 的去抖间隔(默认 200ms),把一次保存产生的多次事件合并。
func WithWatchDelay(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.watchDelay = d
		}
	}
}
