package jsvm

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/dop251/goja"
)

// bindHTTP 绑定 $http.send(config):
//
//	const res = $http.send({ url, method: "POST", headers: {...}, body: {...} | "text" | ArrayBuffer, timeout: 10 })
//	res.statusCode / res.headers(小写键,取首个值) / res.raw(响应体字符串) / res.json(可解析时为对象,否则 null)
//
// body 为普通对象时按 JSON 发送并默认 Content-Type: application/json。timeout 单位秒。
// 请求继承当前调用的 ctx:HTTP 请求被取消、调用超时都会中止外发请求。非 2xx 不抛异常,网络错误才抛。
func (inst *instance) bindHTTP() error {
	rt := inst.rt
	obj := rt.NewObject()
	if err := obj.Set("send", func(cfg *goja.Object) *goja.Object {
		if cfg == nil {
			panic(rt.NewTypeError("$http.send: config object is required"))
		}
		url := cfg.Get("url")
		if !isSet(url) {
			panic(rt.NewTypeError("$http.send: url is required"))
		}
		method := http.MethodGet
		if m := cfg.Get("method"); isSet(m) {
			method = strings.ToUpper(m.String())
		}

		var body io.Reader
		jsonBody := false
		if b := cfg.Get("body"); isSet(b) {
			data, ok := inst.toBytes(b)
			if !ok {
				data, jsonBody = []byte(inst.toJSON(b)), true
			}
			body = bytes.NewReader(data)
		}

		var timeout float64
		if t := cfg.Get("timeout"); isSet(t) {
			timeout = t.ToFloat()
		}
		ctx, cancel := inst.callCtx(timeout)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, method, url.String(), body)
		if err != nil {
			inst.throw(err)
		}
		if h, ok := cfg.Get("headers").(*goja.Object); ok {
			for _, k := range h.Keys() {
				req.Header.Set(k, h.Get(k).String())
			}
		}
		if jsonBody && req.Header.Get("Content-Type") == "" {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := inst.vm.cfg.httpClient.Do(req)
		if err != nil {
			inst.throw(err)
		}
		defer resp.Body.Close()
		limit := inst.vm.cfg.maxBodySize
		raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
		if err != nil {
			inst.throw(err)
		}
		if int64(len(raw)) > limit {
			inst.throw(fmt.Errorf("$http.send: response body exceeds %d bytes", limit))
		}

		headers := rt.NewObject()
		for k, v := range resp.Header {
			_ = headers.Set(strings.ToLower(k), v[0])
		}
		var parsed goja.Value = goja.Null()
		if len(raw) > 0 {
			if v, err := inst.parse(goja.Undefined(), rt.ToValue(string(raw))); err == nil {
				parsed = v
			}
		}
		res := rt.NewObject()
		_ = res.Set("statusCode", resp.StatusCode)
		_ = res.Set("headers", headers)
		_ = res.Set("raw", string(raw))
		_ = res.Set("json", parsed)
		return res
	}); err != nil {
		return err
	}
	return rt.Set("$http", obj)
}
