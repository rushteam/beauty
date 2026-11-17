package mdrt

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/rushteam/beauty/contrib/jsvm"
	"github.com/rushteam/beauty/pkg/messaging/eventbus"
	"github.com/rushteam/beauty/pkg/security"
	"github.com/rushteam/beauty/pkg/store/blob"
)

// failure 是一条验收失败,Slug 为空表示无法归因到单个文件。
type failure struct {
	Slug    string
	Unit    int
	Example string
	Message string
}

func (f failure) String() string {
	if f.Slug == "" {
		return f.Message
	}
	return fmt.Sprintf("%s 例子 %s: %s", f.Slug, f.Example, f.Message)
}

func (rt *Runtime) verify(ctx context.Context, files map[string]*goodFile) []failure {
	if len(files) == 0 {
		return nil
	}
	snap := filesOf(files)
	fsys := newGenFS()
	fsys.Store(snap)
	stubs := &stubTransport{}
	conf := &HostConf{JwtSecret: rt.conf.JwtSecret, NowMs: clockOf(files)}
	vm, err := jsvm.New(
		jsvm.WithFS(fsys, "*.js"),
		jsvm.WithPoolSize(2),
		jsvm.WithTimeout(2*time.Second),
		jsvm.WithEventBus(eventbus.New[any]()),
		jsvm.WithBucket(blob.NewMemory()),
		jsvm.WithHTTPClient(&http.Client{Transport: stubs, Timeout: 2 * time.Second}),
		jsvm.WithGlobal("__mdrtConf", conf),
		jsvm.WithLogger(rt.log),
	)
	if err != nil {
		return blameLoad(err, files)
	}
	defer vm.Close()

	var fails []failure
	for _, g := range files {
		if g == nil || g.spec == nil {
			continue
		}
		for i, u := range g.spec.Units {
			if u.Pending {
				continue
			}
			for _, ex := range u.Examples {
				if err := runExample(ctx, vm, stubs, rt.conf.JwtSecret, g.spec, i, u, ex); err != nil {
					fails = append(fails, failure{
						Slug: g.spec.Slug, Unit: i, Example: ex.Title, Message: err.Error(),
					})
				}
			}
		}
	}
	return fails
}

func blameLoad(err error, files map[string]*goodFile) []failure {
	msg := err.Error()
	var hits []failure
	for slug := range files {
		if strings.Contains(msg, slug+".js") {
			hits = append(hits, failure{Slug: slug, Message: msg})
		}
	}
	if len(hits) == 0 {
		return []failure{{Message: msg}}
	}
	return hits
}

func clockOf(files map[string]*goodFile) int64 {
	var clock int64
	set := false
	for _, g := range files {
		if g == nil || g.spec == nil || g.spec.NowMs == 0 {
			continue
		}
		if !set {
			clock, set = g.spec.NowMs, true
			continue
		}
		if clock != g.spec.NowMs {
			return 0
		}
	}
	return clock
}

func runExample(ctx context.Context, vm *jsvm.VM, stubs *stubTransport, secret string, spec *Spec, idx int, u Unit, ex Example) error {
	list := append([]Stub(nil), ex.Stubs...)
	stubs.cur.Store(&list)
	if err := resetStore(ctx, vm, ex.Store); err != nil {
		return err
	}
	switch u.Kind {
	case KindRoute:
		return runRoute(vm, secret, ex)
	default:
		return runHook(ctx, vm, spec.Slug, idx, u, ex)
	}
}

func resetStore(ctx context.Context, vm *jsvm.VM, store map[string]any) error {
	if store == nil {
		return vm.Trigger(ctx, "mdrt:reset", map[string]any{})
	}
	raw, err := json.Marshal(store)
	if err != nil {
		return err
	}
	return vm.Trigger(ctx, "mdrt:seed", map[string]any{"json": string(raw)})
}

func runRoute(vm *jsvm.VM, secret string, ex Example) error {
	if ex.Req == nil || ex.Res == nil {
		return fmt.Errorf("缺少 req/res")
	}
	var body io.Reader
	if ex.Req.Body != "" {
		body = strings.NewReader(ex.Req.Body)
	}
	req := httptest.NewRequest(ex.Req.Method, ex.Req.URL, body)
	for k, v := range ex.Req.Header {
		replaced, err := replaceTokens(v, secret)
		if err != nil {
			return err
		}
		req.Header.Set(k, replaced)
	}
	rr := httptest.NewRecorder()
	vm.ServeHTTP(rr, req)
	if rr.Code != ex.Res.Status {
		return fmt.Errorf("状态码 %d,期望 %d,正文 %s", rr.Code, ex.Res.Status, strings.TrimSpace(rr.Body.String()))
	}
	if err := matchBody(rr.Body.String(), ex.Res.Body); err != nil {
		return err
	}
	return matchStore(context.Background(), vm, ex.StoreAfter)
}

func runHook(ctx context.Context, vm *jsvm.VM, slug string, idx int, u Unit, ex Example) error {
	ev := map[string]any{}
	if ex.Event != "" {
		ev["json"] = ex.Event
	} else {
		ev["json"] = "{}"
	}
	err := vm.Trigger(ctx, fmt.Sprintf("mdrt:unit:%s:%d", slug, idx), ev)
	if ex.Res != nil && ex.Res.Status >= 400 {
		je, ok := err.(*jsvm.Error)
		if !ok || je.Status != ex.Res.Status {
			return fmt.Errorf("期望状态 %d,得到 %v", ex.Res.Status, err)
		}
		return matchStore(ctx, vm, ex.StoreAfter)
	}
	if err != nil {
		return err
	}
	if ex.EventAfter != "" {
		out, _ := ev["out"].(string)
		if err := matchBody(out, ex.EventAfter); err != nil {
			return fmt.Errorf("事件结果 %w", err)
		}
	}
	return matchStore(ctx, vm, ex.StoreAfter)
}

func matchStore(ctx context.Context, vm *jsvm.VM, want map[string]any) error {
	if want == nil {
		return nil
	}
	ev := map[string]any{}
	if err := vm.Trigger(ctx, "mdrt:dump", ev); err != nil {
		return err
	}
	raw, _ := ev["json"].(string)
	var got map[string]any
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &got); err != nil {
			return err
		}
	}
	if !subset(got, want) {
		return fmt.Errorf("store 得到 %s,期望包含 %v", raw, want)
	}
	return nil
}

func matchBody(got, want string) error {
	want = strings.TrimSpace(want)
	if want == "" {
		return nil
	}
	if w, ok := parseJSONValue(want); ok {
		g, ok := parseJSONValue(got)
		if !ok || !subset(g, w) {
			return fmt.Errorf("正文 %s,期望包含 %s", strings.TrimSpace(got), want)
		}
		return nil
	}
	if strings.TrimSpace(got) != want {
		return fmt.Errorf("正文 %q,期望 %q", strings.TrimSpace(got), want)
	}
	return nil
}

func replaceTokens(v, secret string) (string, error) {
	var err error
	out := tokenRe.ReplaceAllStringFunc(v, func(m string) string {
		sub := tokenRe.FindStringSubmatch(m)
		if len(sub) != 2 {
			return m
		}
		if secret == "" {
			err = fmt.Errorf("例子使用了 $token,但没有配置 JWT 密钥")
			return m
		}
		tok, e := security.CreateJWT(map[string]any{"sub": sub[1]}, []byte(secret), time.Hour)
		if e != nil {
			err = e
			return m
		}
		return tok
	})
	return out, err
}
