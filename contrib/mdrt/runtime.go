package mdrt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/rushteam/beauty/contrib/jsvm"
	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/pkg/foundation/syncx"
	"github.com/rushteam/beauty/pkg/messaging/eventbus"
)

// HostConf 注入到脚本的全局配置。goja 会把导出字段映射成小写开头。
// JwtSecret 在脚本里是 jwtSecret。不要写成 JWTSecret,goja 只会把首字母改成小写。
type HostConf struct {
	JwtSecret string
	NowMs     int64
}

// FileStatus 是一个 spec 当前的编译状态。
type FileStatus struct {
	File        string       `json:"file"`
	Slug        string       `json:"slug"`
	Generation  uint64       `json:"generation"`
	State       string       `json:"state"`
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
}

// Stats 是进程内累计的编译计数。
type Stats struct {
	Commits      uint64
	Rollbacks    uint64
	LLMCalls     uint64
	CacheHits    uint64
	Repairs      uint64
	InputTokens  uint64
	OutputTokens uint64
	LastCommit   time.Duration
}

// Option 配置 Runtime。
type Option func(*config)

type config struct {
	specDir   string
	stateDir  string
	lockFile  string
	mode      Mode
	llm       llm.Client
	model     string
	jwtSecret string
	logger    *slog.Logger
	debounce  time.Duration
	jsvm      []jsvm.Option
	parallel  int
	bus       *eventbus.Bus[any]
}

func defaultConfig() config {
	return config{
		specDir:  "specs",
		stateDir: ".mdrt",
		lockFile: "mdrt.lock.json",
		mode:     ModeDev,
		debounce: 300 * time.Millisecond,
		parallel: 4,
		logger:   slog.Default(),
	}
}

// WithSpecDir 设置 Markdown 目录。
func WithSpecDir(dir string) Option { return func(c *config) { c.specDir = dir } }

// WithStateDir 设置缓存、生成物和诊断的目录。
func WithStateDir(dir string) Option { return func(c *config) { c.stateDir = dir } }

// WithLockFile 设置锁文件路径。
func WithLockFile(path string) Option { return func(c *config) { c.lockFile = path } }

// WithMode 设置运行模式。
func WithMode(m Mode) Option { return func(c *config) { c.mode = m } }

// WithLLM 设置模型客户端。不设置时行为段只会走缓存或占位实现。
func WithLLM(client llm.Client, model string) Option {
	return func(c *config) {
		c.llm = client
		if model != "" {
			c.model = model
		}
	}
}

// WithModel 设置参与缓存键的模型名。离线构建要和当初编译时用同一个名字。
func WithModel(model string) Option { return func(c *config) { c.model = model } }

// WithJWTSecret 设置 bearer 例子和线上鉴权共用的 HS256 密钥。
func WithJWTSecret(secret string) Option { return func(c *config) { c.jwtSecret = secret } }

// WithLogger 设置日志。
func WithLogger(l *slog.Logger) Option {
	return func(c *config) {
		if l != nil {
			c.logger = l
		}
	}
}

// WithDebounce 设置保存后的去抖间隔。
func WithDebounce(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.debounce = d
		}
	}
}

// WithEventBus 设置线上脚本使用的事件总线。
func WithEventBus(bus *eventbus.Bus[any]) Option { return func(c *config) { c.bus = bus } }

// WithJSVM 把额外选项传给线上 VM。WithOS、WithWatch、WithFS、WithHTTPClient 会被运行时覆盖。
func WithJSVM(opts ...jsvm.Option) Option {
	return func(c *config) { c.jsvm = append(c.jsvm, opts...) }
}

// Runtime 监听 Markdown,编译,验收,再热切换一个 jsvm。
type Runtime struct {
	cfg    config
	log    *slog.Logger
	cache  diskCache
	flight syncx.Group[string]
	sem    chan struct{}
	typed  *llm.TypedOutput[behaviorOut]
	gen    *genFS
	conf   *HostConf
	bus    *eventbus.Bus[any]
	allow  atomic.Pointer[hostAllow]
	client *http.Client

	mu         sync.Mutex
	live       atomic.Pointer[jsvm.VM]
	good       map[string]*goodFile
	status     map[string]*FileStatus
	seq        map[string]uint64
	seen       map[string]uint64
	done       map[string]string
	generation uint64

	commitCh chan fileResult

	llmCalls   atomic.Uint64
	inTokens   atomic.Uint64
	outTokens  atomic.Uint64
	commits    atomic.Uint64
	rollbacks  atomic.Uint64
	cacheHits  atomic.Uint64
	repairs    atomic.Uint64
	lastCommit atomic.Int64
}

type goodFile struct {
	path       string
	source     []byte
	js         string
	spec       *Spec
	sourceHash string
	pending    bool
	units      []unitCode
	diags      Diagnostics
}

type fileResult struct {
	path       string
	slug       string
	source     []byte
	sourceHash string
	js         string
	spec       *Spec
	units      []unitCode
	diags      Diagnostics
	pending    bool
	delete     bool
	seq        uint64
	parse      time.Duration
	compile    time.Duration
	lower      time.Duration
}

func (r fileResult) toGood() *goodFile {
	return &goodFile{
		path: r.path, source: r.source, js: r.js, spec: r.spec,
		sourceHash: r.sourceHash, pending: r.pending, units: r.units, diags: r.diags,
	}
}

type produceFlags struct {
	sync   bool
	strict bool
	hint   string
}

// New 准备运行时,不启动监听。Start 或 Build 才会读 spec。
func New(opts ...Option) (*Runtime, error) {
	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.specDir == "" {
		return nil, fmt.Errorf("mdrt: 缺少 spec 目录")
	}
	if st, err := os.Stat(cfg.specDir); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("mdrt: spec 目录 %s 不存在", cfg.specDir)
	}
	if cfg.bus == nil {
		cfg.bus = eventbus.New[any]()
	}
	typed, err := llm.NewTypedOutput[behaviorOut]("mdrt_behavior")
	if err != nil {
		typed = nil
	}
	rt := &Runtime{
		cfg:      cfg,
		log:      cfg.logger,
		cache:    diskCache{dir: filepath.Join(cfg.stateDir, "cache")},
		sem:      make(chan struct{}, cfg.parallel),
		typed:    typed,
		gen:      newGenFS(),
		conf:     &HostConf{JwtSecret: cfg.jwtSecret},
		bus:      cfg.bus,
		good:     map[string]*goodFile{},
		status:   map[string]*FileStatus{},
		seq:      map[string]uint64{},
		seen:     map[string]uint64{},
		done:     map[string]string{},
		commitCh: make(chan fileResult, 64),
	}
	rt.allow.Store(&hostAllow{hosts: map[string]struct{}{}})
	rt.client = &http.Client{
		Timeout: 30 * time.Second,
		Transport: &allowTransport{
			base:  http.DefaultTransport,
			allow: &rt.allow,
			log:   rt.log,
		},
	}
	return rt, nil
}

// EventBus 返回线上脚本使用的事件总线。
func (rt *Runtime) EventBus() *eventbus.Bus[any] { return rt.bus }

// Start 编译当前 spec,启动线上 VM,并在 dev/offline 模式下监听文件。ctx 结束时返回。
func (rt *Runtime) Start(ctx context.Context) error {
	if err := rt.boot(ctx); err != nil {
		return err
	}
	go rt.commitLoop(ctx)
	if rt.cfg.mode != ModeBuild {
		go rt.watch(ctx)
		go rt.compilePending(ctx)
	}
	vm := rt.live.Load()
	if vm == nil {
		<-ctx.Done()
		return nil
	}
	return vm.Start(ctx)
}

func (rt *Runtime) String() string { return "mdrt" }

// ServeHTTP 把请求交给当前一代脚本。Start 之前返回 503。
func (rt *Runtime) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	vm := rt.live.Load()
	if vm == nil {
		http.Error(w, "mdrt: not started", http.StatusServiceUnavailable)
		return
	}
	vm.ServeHTTP(w, r)
}

// Status 返回每个 spec 的状态副本。
func (rt *Runtime) Status() []FileStatus {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	out := make([]FileStatus, 0, len(rt.status))
	for _, st := range rt.status {
		cp := *st
		cp.Diagnostics = append([]Diagnostic(nil), st.Diagnostics...)
		out = append(out, cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].File < out[j].File })
	return out
}

// Stats 返回累计计数。
func (rt *Runtime) Stats() Stats {
	return Stats{
		Commits:      rt.commits.Load(),
		Rollbacks:    rt.rollbacks.Load(),
		LLMCalls:     rt.llmCalls.Load(),
		CacheHits:    rt.cacheHits.Load(),
		Repairs:      rt.repairs.Load(),
		InputTokens:  rt.inTokens.Load(),
		OutputTokens: rt.outTokens.Load(),
		LastCommit:   time.Duration(rt.lastCommit.Load()),
	}
}

func (rt *Runtime) boot(ctx context.Context) error {
	batch, err := rt.readAll(ctx, produceFlags{sync: false, strict: rt.cfg.mode == ModeBuild && rt.cfg.llm == nil})
	if err != nil {
		return err
	}
	return rt.apply(ctx, batch, applyOpt{strict: rt.cfg.mode == ModeBuild})
}

func (rt *Runtime) readAll(ctx context.Context, flags produceFlags) ([]fileResult, error) {
	entries, err := os.ReadDir(rt.cfg.specDir)
	if err != nil {
		return nil, err
	}
	var batch []fileResult
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(rt.cfg.specDir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		res := rt.translate(ctx, path, raw, flags)
		if flags.strict && res.js == "" {
			if len(res.diags) > 0 {
				return nil, res.diags
			}
			return nil, fmt.Errorf("mdrt: %s 没有生成脚本", e.Name())
		}
		batch = append(batch, res)
	}
	return batch, nil
}

func (rt *Runtime) syncLLM() bool {
	return rt.cfg.llm != nil && rt.cfg.mode != ModeOffline
}

func (rt *Runtime) translate(ctx context.Context, path string, raw []byte, flags produceFlags) fileResult {
	started := time.Now()
	base := filepath.Base(path)
	res := fileResult{
		path: path, slug: strings.TrimSuffix(base, ".md"), source: append([]byte(nil), raw...),
		sourceHash: hashBytes(raw),
	}
	t0 := time.Now()
	spec, diags := Parse(base, string(raw))
	res.parse = time.Since(t0)
	if spec == nil || len(diags) > 0 {
		res.diags = diags
		return res
	}
	res.spec = spec
	res.slug = spec.Slug
	c0 := time.Now()
	bodies := make([]unitCode, len(spec.Units))
	for i := range spec.Units {
		code, err := rt.codeFor(ctx, spec, spec.Units[i], flags)
		if err != nil {
			res.diags = append(res.diags, errorDiag(base, spec.Units[i].Line, "%s", err.Error()))
			res.compile = time.Since(c0)
			return res
		}
		bodies[i] = code
		spec.Units[i].Pending = code.Pending
		if code.Pending {
			res.pending = true
			res.diags = append(res.diags, Diagnostic{
				File: base, Line: spec.Units[i].Line, Severity: "warning",
				Message: "行为段尚未编译,路由会返回 503",
			})
		}
	}
	res.compile = time.Since(c0)
	res.units = bodies
	l0 := time.Now()
	js, err := Lower(spec, bodies)
	res.lower = time.Since(l0)
	if err != nil {
		res.diags = append(res.diags, errorDiag(base, 1, "%s", err.Error()))
		return res
	}
	res.js = js
	res.parse = time.Since(started) - res.compile - res.lower
	return res
}

func (rt *Runtime) codeFor(ctx context.Context, spec *Spec, unit Unit, flags produceFlags) (unitCode, error) {
	hint := flags.hint
	attempts := 1
	if flags.hint == "" && flags.sync && rt.cfg.llm != nil && unit.Behavior != nil {
		attempts = 3
	}
	var last error
	for i := 0; i < attempts; i++ {
		body, pending, key, err := rt.once(ctx, spec, unit, flags.sync, flags.strict, hint)
		if err == nil {
			return unitCode{Body: body, Pending: pending, CacheKey: key}, nil
		}
		last = err
		hint = err.Error()
		if i+1 < attempts {
			rt.repairs.Add(1)
		}
	}
	return unitCode{}, last
}

func (rt *Runtime) once(ctx context.Context, spec *Spec, unit Unit, sync, strict bool, hint string) (string, bool, string, error) {
	if unit.Impl != nil {
		if err := Check(unit.Kind, unit.Impl.Body); err != nil {
			return "", false, "", err
		}
		return unit.Impl.Body, false, "", nil
	}
	key := CacheKey(rt.cfg.model, unit)
	if hint == "" {
		if ent, ok := rt.cache.Get(key); ok && Check(unit.Kind, ent.Body) == nil {
			rt.cacheHits.Add(1)
			return ent.Body, false, key, nil
		}
	}
	if !sync || rt.cfg.llm == nil {
		if strict {
			short := key
			if len(short) > 8 {
				short = short[:8]
			}
			return "", false, key, fmt.Errorf("缓存缺失 %s", short)
		}
		return PlaceholderBody, true, key, nil
	}
	body, _, err := rt.generate(ctx, spec, unit, hint)
	if err != nil {
		return "", false, key, err
	}
	return body, false, key, nil
}

type applyOpt struct {
	strict bool
}

func (rt *Runtime) apply(ctx context.Context, batch []fileResult, opt applyOpt) error {
	rt.mu.Lock()
	cand := cloneGoods(rt.good)
	var accepted []fileResult
	for _, res := range batch {
		if res.slug == "" {
			continue
		}
		if res.seq > 0 && res.seq < rt.seen[res.slug] {
			continue
		}
		if res.seq > 0 {
			rt.seen[res.slug] = res.seq
		}
		accepted = append(accepted, res)
	}
	rt.mu.Unlock()

	merged := map[string]fileResult{}
	for _, res := range accepted {
		merged[res.slug] = res
	}
	for slug, res := range merged {
		switch {
		case res.delete:
			delete(cand, slug)
			rt.mu.Lock()
			delete(rt.status, slug)
			delete(rt.done, res.path)
			rt.mu.Unlock()
		case res.js == "":
			rt.note(slug, res, "failed")
		default:
			cand[slug] = res.toGood()
		}
	}
	fails := rt.verify(ctx, cand)
	if len(fails) > 0 {
		cand = rt.repairOrRevert(ctx, cand, fails)
		if opt.strict {
			for _, res := range accepted {
				if res.delete || res.js == "" {
					continue
				}
				g := cand[res.slug]
				if g == nil || g.sourceHash != res.sourceHash {
					return fmt.Errorf("mdrt: %s 未通过验收: %s", res.slug, formatFails(fails))
				}
			}
		}
	}
	return rt.publish(ctx, cand)
}

func (rt *Runtime) repairOrRevert(ctx context.Context, cand map[string]*goodFile, fails []failure) map[string]*goodFile {
	rt.mu.Lock()
	base := cloneGoods(rt.good)
	rt.mu.Unlock()
	if rt.syncLLM() {
		for round := 0; round < 2 && len(fails) > 0; round++ {
			blamed := blameSlugs(fails, cand)
			changed := false
			for _, slug := range blamed {
				g := cand[slug]
				if g == nil || g.spec == nil || !hasBehavior(g.spec) {
					continue
				}
				res := rt.translate(ctx, g.path, g.source, produceFlags{
					sync: true,
					hint: formatFails(filterSlug(fails, slug)),
				})
				if res.js == "" {
					continue
				}
				cand[slug] = res.toGood()
				changed = true
			}
			if !changed {
				break
			}
			rt.repairs.Add(1)
			fails = rt.verify(ctx, cand)
		}
	}
	if len(fails) == 0 {
		return cand
	}
	blamed := blameSlugs(fails, cand)
	if len(blamed) == 0 {
		rt.rollbacks.Add(1)
		return base
	}
	for _, slug := range blamed {
		if old, ok := base[slug]; ok {
			cand[slug] = old
		} else {
			delete(cand, slug)
		}
		rt.rollbacks.Add(1)
		rt.note(slug, fileResult{
			slug: slug, path: slug + ".md",
			diags: Diagnostics{errorDiag(slug+".md", 1, "%s", formatFails(filterSlug(fails, slug)))},
		}, "failed")
	}
	if again := rt.verify(ctx, cand); len(again) > 0 {
		rt.rollbacks.Add(1)
		return base
	}
	return cand
}

func hasBehavior(spec *Spec) bool {
	for _, u := range spec.Units {
		if u.Behavior != nil && !u.Pending {
			return true
		}
	}
	return false
}

func (rt *Runtime) publish(ctx context.Context, cand map[string]*goodFile) error {
	snap := filesOf(cand)
	var specs []*Spec
	for _, g := range cand {
		if g != nil && g.spec != nil {
			specs = append(specs, g.spec)
		}
	}
	rt.allow.Store(allowOf(specs))
	prev := rt.gen.cur.Load()
	rt.gen.Store(snap)
	started := time.Now()
	if rt.live.Load() == nil {
		vm, err := rt.newLive()
		if err != nil {
			rt.gen.Store(prev)
			return err
		}
		rt.live.Store(vm)
	} else if err := rt.live.Load().Reload(ctx); err != nil {
		rt.gen.Store(prev)
		rt.log.Error("mdrt: 热切换失败", slog.Any("err", err))
		return err
	}
	rt.mu.Lock()
	rt.good = cand
	rt.generation++
	gen := rt.generation
	for slug, g := range cand {
		state := "ok"
		if g.pending {
			state = "pending"
		}
		rt.status[slug] = &FileStatus{
			File: filepath.Base(g.path), Slug: slug, Generation: gen,
			State: state, Diagnostics: append([]Diagnostic(nil), g.diags...),
		}
		rt.done[g.path] = g.sourceHash
	}
	rt.mu.Unlock()
	rt.commits.Add(1)
	rt.lastCommit.Store(int64(time.Since(started)))
	if err := rt.writeSide(cand); err != nil {
		rt.log.Error("mdrt: 写生成物失败", slog.Any("err", err))
	}
	rt.log.Info("mdrt: 换代",
		slog.Uint64("gen", gen),
		slog.Int("files", len(cand)),
		slog.Duration("reload", time.Since(started)),
	)
	return nil
}

func (rt *Runtime) note(slug string, res fileResult, state string) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	file := res.path
	if file == "" {
		file = slug + ".md"
	}
	rt.status[slug] = &FileStatus{
		File: filepath.Base(file), Slug: slug, Generation: rt.generation,
		State: state, Diagnostics: append([]Diagnostic(nil), res.diags...),
	}
}

func (rt *Runtime) newLive() (*jsvm.VM, error) {
	opts := []jsvm.Option{
		jsvm.WithPoolSize(2),
		jsvm.WithTimeout(5 * time.Second),
		jsvm.WithMaxBodySize(1 << 20),
		jsvm.WithLogger(rt.log),
	}
	opts = append(opts, rt.cfg.jsvm...)
	opts = append(opts,
		jsvm.WithOS(false),
		jsvm.WithWatch(false),
		jsvm.WithFS(rt.gen, "*.js"),
		jsvm.WithEventBus(rt.bus),
		jsvm.WithHTTPClient(rt.client),
		jsvm.WithGlobal("__mdrtConf", rt.conf),
	)
	return jsvm.New(opts...)
}

func (rt *Runtime) writeSide(cand map[string]*goodFile) error {
	if err := rt.writeGen(filesOf(cand)); err != nil {
		return err
	}
	if err := rt.writeCache(cand); err != nil {
		return err
	}
	if err := rt.writeLock(cand); err != nil {
		return err
	}
	return rt.writeDiags()
}

func (rt *Runtime) writeGen(snap *snapshot) error {
	dir := filepath.Join(rt.cfg.stateDir, "gen")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	keep := map[string]bool{}
	for name, body := range snap.files {
		keep[name] = true
		if err := writeAtomic(filepath.Join(dir, name), []byte(body)); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !keep[e.Name()] && strings.HasSuffix(e.Name(), ".js") {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
	return nil
}

func (rt *Runtime) writeCache(files map[string]*goodFile) error {
	for _, g := range files {
		for _, u := range g.units {
			if u.CacheKey == "" || u.Pending {
				continue
			}
			if err := rt.cache.Put(u.CacheKey, cacheEntry{
				Body: u.Body, Model: rt.cfg.model, Time: nowStamp(),
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

type lockDoc struct {
	Files map[string]lockSpec `json:"files"`
}

type lockSpec struct {
	SourceHash string     `json:"sourceHash"`
	Units      []lockUnit `json:"units"`
}

type lockUnit struct {
	Index    int    `json:"index"`
	Kind     string `json:"kind"`
	CacheKey string `json:"cacheKey,omitempty"`
	BodyHash string `json:"bodyHash"`
	Pending  bool   `json:"pending,omitempty"`
}

func (rt *Runtime) writeLock(files map[string]*goodFile) error {
	doc := lockDoc{Files: map[string]lockSpec{}}
	for slug, g := range files {
		spec := lockSpec{SourceHash: g.sourceHash, Units: []lockUnit{}}
		if g.spec != nil {
			for i, u := range g.spec.Units {
				item := lockUnit{Index: i, Kind: u.Kind, Pending: u.Pending}
				if i < len(g.units) {
					item.CacheKey = g.units[i].CacheKey
					item.BodyHash = bodyHash(g.units[i].Body)
					item.Pending = g.units[i].Pending
				}
				spec.Units = append(spec.Units, item)
			}
		}
		doc.Files[slug] = spec
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(rt.cfg.lockFile, append(raw, '\n'))
}

func (rt *Runtime) writeDiags() error {
	rt.mu.Lock()
	var all []Diagnostic
	for _, st := range rt.status {
		all = append(all, st.Diagnostics...)
	}
	rt.mu.Unlock()
	raw, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(rt.cfg.stateDir, "diagnostics.json"), append(raw, '\n'))
}

func (rt *Runtime) commitLoop(ctx context.Context) {
	var batch []fileResult
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case res := <-rt.commitCh:
			batch = append(batch, res)
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(30 * time.Millisecond)
		case <-timer.C:
			if len(batch) == 0 {
				continue
			}
			got := batch
			batch = nil
			if err := rt.apply(ctx, got, applyOpt{}); err != nil {
				rt.log.Error("mdrt: 提交失败", slog.Any("err", err))
			}
		}
	}
}

func (rt *Runtime) watch(ctx context.Context) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		rt.log.Error("mdrt: 监听失败", slog.Any("err", err))
		return
	}
	defer w.Close()
	if err := w.Add(rt.cfg.specDir); err != nil {
		rt.log.Error("mdrt: 监听目录失败", slog.Any("err", err))
		return
	}
	timers := map[string]*time.Timer{}
	var mu sync.Mutex
	stop := func() {
		mu.Lock()
		defer mu.Unlock()
		for _, t := range timers {
			t.Stop()
		}
	}
	defer stop()
	fire := func(name string) {
		if !strings.HasSuffix(name, ".md") {
			return
		}
		path := filepath.Join(rt.cfg.specDir, filepath.Base(name))
		raw, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				rt.enqueue(ctx, fileResult{
					path: path, slug: strings.TrimSuffix(filepath.Base(name), ".md"), delete: true,
				})
			}
			return
		}
		sum := hashBytes(raw)
		rt.mu.Lock()
		same := rt.done[path] == sum
		rt.mu.Unlock()
		if same {
			return
		}
		res := rt.translate(ctx, path, raw, produceFlags{sync: rt.syncLLM()})
		if ctx.Err() != nil {
			return
		}
		rt.enqueue(ctx, res)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			rt.log.Warn("mdrt: 监听错误", slog.Any("err", err))
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			if ev.Op&fsnotify.Chmod == ev.Op {
				continue
			}
			base := filepath.Base(ev.Name)
			if !strings.HasSuffix(base, ".md") {
				continue
			}
			mu.Lock()
			if t := timers[base]; t != nil {
				t.Stop()
			}
			timers[base] = time.AfterFunc(rt.cfg.debounce, func() { fire(base) })
			mu.Unlock()
		}
	}
}

func (rt *Runtime) enqueue(ctx context.Context, res fileResult) {
	rt.mu.Lock()
	rt.seq[res.slug]++
	res.seq = rt.seq[res.slug]
	rt.mu.Unlock()
	select {
	case <-ctx.Done():
	case rt.commitCh <- res:
	}
}

func (rt *Runtime) compilePending(ctx context.Context) {
	if !rt.syncLLM() {
		return
	}
	rt.mu.Lock()
	var pending []*goodFile
	for _, g := range rt.good {
		if g.pending {
			pending = append(pending, g)
		}
	}
	rt.mu.Unlock()
	for _, g := range pending {
		if ctx.Err() != nil {
			return
		}
		res := rt.translate(ctx, g.path, g.source, produceFlags{sync: true})
		if ctx.Err() != nil {
			return
		}
		rt.enqueue(ctx, res)
	}
}

func filesOf(files map[string]*goodFile) *snapshot {
	out := map[string]string{PreludeFile: preludeSource}
	for slug, g := range files {
		if g != nil && g.js != "" {
			out[slug+".js"] = g.js
		}
	}
	return &snapshot{files: out}
}

func cloneGoods(in map[string]*goodFile) map[string]*goodFile {
	out := make(map[string]*goodFile, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func blameSlugs(fails []failure, cand map[string]*goodFile) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range fails {
		if f.Slug == "" || seen[f.Slug] {
			continue
		}
		seen[f.Slug] = true
		out = append(out, f.Slug)
	}
	if len(out) == 0 {
		for slug := range cand {
			out = append(out, slug)
		}
	}
	sort.Strings(out)
	return out
}

func filterSlug(fails []failure, slug string) []failure {
	var out []failure
	for _, f := range fails {
		if f.Slug == slug || f.Slug == "" {
			out = append(out, f)
		}
	}
	return out
}

func formatFails(fails []failure) string {
	var b strings.Builder
	for i, f := range fails {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(f.String())
	}
	return b.String()
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
