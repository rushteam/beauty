package jsvm

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/dop251/goja"
)

// requestEvent 是传给路由处理函数 / 中间件的 e。
//
//	e.request: method / url / path / host / remoteAddr / proto,
//	           header(name) / headers() / query(name) / pathValue(name) / cookie(name) / formValue(name) / text() / json()
//	e.json(status, data) / e.string(status, text) / e.html(status, html) / e.blob(status, contentType, data)
//	e.noContent(status) / e.redirect(status, url) / e.setHeader(name, value)
//	e.set(key, value) / e.get(key)   请求内传值(如认证中间件写入用户)
//	e.next()                         中间件中调用以继续执行后续中间件 / 处理函数
type requestEvent struct {
	inst    *instance
	w       http.ResponseWriter
	r       *http.Request
	chain   []int
	pos     int
	written bool
	body    []byte
	read    bool
	values  map[string]goja.Value
	obj     *goja.Object
}

func (vm *VM) routeHandler(st *runtimeState, chain []int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := vm.withInstanceState(r.Context(), st, func(inst *instance) error {
			ev := newRequestEvent(inst, w, r, chain)
			if _, err := inst.call(r.Context(), inst.fns[chain[0]], ev.obj); err != nil {
				vm.writeError(ev, inst.toError(err))
			}
			return nil
		})
		if err != nil {
			// 借不到实例:请求已取消或超时。
			http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		}
	})
}

type errorBody struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// writeError 把脚本异常写成 JSON:ApiError 用其 status/message/data,其他异常一律 500 且不泄露细节。
func (vm *VM) writeError(ev *requestEvent, err error) {
	var je *Error
	status := http.StatusInternalServerError
	body := errorBody{Code: status, Message: "internal server error"}
	if errors.As(err, &je) && je.Status >= 400 && je.Status <= 599 {
		status = je.Status
		body = errorBody{Code: je.Status, Message: je.Message, Data: je.Data}
	}
	if status >= 500 {
		vm.cfg.logger.ErrorContext(ev.r.Context(), "jsvm: route handler failed",
			slog.String("method", ev.r.Method), slog.String("path", ev.r.URL.Path), slog.Any("err", err))
	}
	if ev.written {
		return
	}
	ev.written = true
	ev.w.Header().Set("Content-Type", "application/json; charset=utf-8")
	ev.w.WriteHeader(status)
	_ = json.NewEncoder(ev.w).Encode(body)
}

func newRequestEvent(inst *instance, w http.ResponseWriter, r *http.Request, chain []int) *requestEvent {
	ev := &requestEvent{inst: inst, w: w, r: r, chain: chain, values: map[string]goja.Value{}}
	rt := inst.rt
	ev.obj = rt.NewObject()
	_ = setAll(ev.obj, map[string]any{
		"request": ev.requestObject(),
		"next": func() goja.Value {
			ev.pos++
			if ev.pos >= len(ev.chain) {
				return goja.Undefined()
			}
			v, err := inst.fns[ev.chain[ev.pos]](goja.Undefined(), ev.obj)
			rethrow(err)
			return v
		},
		"json": func(status int, data goja.Value) {
			ev.write(status, "application/json; charset=utf-8", []byte(inst.toJSON(data)))
		},
		"string": func(status int, s string) {
			ev.write(status, "text/plain; charset=utf-8", []byte(s))
		},
		"html": func(status int, s string) {
			ev.write(status, "text/html; charset=utf-8", []byte(s))
		},
		"blob": func(status int, contentType string, data goja.Value) {
			b, ok := inst.toBytes(data)
			if !ok {
				panic(rt.NewTypeError("e.blob: data must be a string or binary"))
			}
			ev.write(status, contentType, b)
		},
		"noContent": func(status int) { ev.write(status, "", nil) },
		"redirect": func(status int, url string) {
			ev.markWritten()
			http.Redirect(w, r, url, status)
		},
		"setHeader": func(name, value string) { w.Header().Set(name, value) },
		"set":       func(key string, v goja.Value) { ev.values[key] = v },
		"get": func(key string) goja.Value {
			if v, ok := ev.values[key]; ok {
				return v
			}
			return goja.Undefined()
		},
	})
	return ev
}

func (ev *requestEvent) markWritten() {
	if ev.written {
		ev.inst.throw(errors.New("response already written"))
	}
	ev.written = true
}

func (ev *requestEvent) write(status int, contentType string, body []byte) {
	ev.markWritten()
	if contentType != "" && ev.w.Header().Get("Content-Type") == "" {
		ev.w.Header().Set("Content-Type", contentType)
	}
	ev.w.WriteHeader(status)
	if len(body) > 0 && ev.r.Method != http.MethodHead {
		_, _ = ev.w.Write(body)
	}
}

func (ev *requestEvent) readBody() []byte {
	if !ev.read {
		ev.read = true
		b, err := io.ReadAll(http.MaxBytesReader(ev.w, ev.r.Body, ev.inst.vm.cfg.maxBodySize))
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			panic(ev.inst.apiError(http.StatusRequestEntityTooLarge, "request body too large"))
		}
		if err != nil {
			ev.inst.throw(err)
		}
		ev.body = b
	}
	return ev.body
}

func (ev *requestEvent) requestObject() *goja.Object {
	r, rt := ev.r, ev.inst.rt
	obj := rt.NewObject()
	_ = setAll(obj, map[string]any{
		"method":     r.Method,
		"url":        r.URL.String(),
		"path":       r.URL.Path,
		"host":       r.Host,
		"remoteAddr": r.RemoteAddr,
		"proto":      r.Proto,
		"header":     func(name string) string { return r.Header.Get(name) },
		"headers": func() *goja.Object {
			h := rt.NewObject()
			for k, v := range r.Header {
				_ = h.Set(strings.ToLower(k), v[0])
			}
			return h
		},
		"query":     func(name string) string { return r.URL.Query().Get(name) },
		"pathValue": r.PathValue,
		"cookie": func(name string) goja.Value {
			c, err := r.Cookie(name)
			if err != nil {
				return goja.Null()
			}
			return rt.ToValue(c.Value)
		},
		"formValue": func(name string) string {
			if r.Form == nil {
				r.Body = http.MaxBytesReader(ev.w, r.Body, ev.inst.vm.cfg.maxBodySize)
			}
			return r.FormValue(name)
		},
		"text": func() string { return string(ev.readBody()) },
		"json": func() goja.Value {
			b := ev.readBody()
			if len(b) == 0 {
				return goja.Null()
			}
			v, err := ev.inst.parse(goja.Undefined(), rt.ToValue(string(b)))
			if err != nil {
				panic(ev.inst.apiError(http.StatusBadRequest, "invalid JSON body"))
			}
			return v
		},
	})
	return obj
}
