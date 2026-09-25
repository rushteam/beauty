// sensitive-middleware 示例:HTTP 敏感词中间件。
//
// 演示 pkg/middleware/sensitive:即插即用地拦截(403)或脱敏(打码)请求体里的敏感词。
// 用 httptest 起临时服务并发几个请求,打印结果后退出(无需手动 Ctrl-C)。
package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/rushteam/beauty/pkg/foundation/actrie"
	"github.com/rushteam/beauty/pkg/middleware/sensitive"
)

// 业务处理器:把请求体原样回写,便于观察脱敏效果。
func echo() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.Write(b)
	})
}

func post(srv *httptest.Server, body string) (int, string) {
	resp, _ := http.Post(srv.URL, "application/json", strings.NewReader(body))
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func main() {
	// ===== 拦截模式:命中即 403 =====
	blockSrv := httptest.NewServer(sensitive.Block("敏感词", "违禁")(echo()))
	defer blockSrv.Close()
	fmt.Println("== 拦截模式(ModeBlock) ==")
	code, _ := post(blockSrv, `{"msg":"正常内容"}`)
	fmt.Printf("正常内容      → %d\n", code)
	code, _ = post(blockSrv, `{"msg":"含违禁内容"}`)
	fmt.Printf("含违禁内容    → %d (拦截)\n\n", code)

	// ===== 脱敏模式:命中打码后放行,业务侧读到干净文本 =====
	maskSrv := httptest.NewServer(sensitive.Mask("敏感词", "违禁")(echo()))
	defer maskSrv.Close()
	fmt.Println("== 脱敏模式(ModeMask) ==")
	code, body := post(maskSrv, `{"msg":"含敏感词和违禁"}`)
	fmt.Printf("含敏感词和违禁 → %d,业务侧读到: %s\n\n", code, body)

	// ===== 反绕过 + 自定义响应 =====
	norm := actrie.Chain(actrie.Keep(func(r rune) bool {
		return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
	}), actrie.FoldCase())
	m := actrie.New(actrie.WithNormalizer(norm))
	m.Add("badword")
	evadeSrv := httptest.NewServer(sensitive.Middleware(sensitive.Config{
		Matcher: m, Mode: sensitive.ModeBlock,
		OnBlocked: func(w http.ResponseWriter, r *http.Request, hit string) {
			fmt.Printf("[审计] 命中敏感词 %q, path=%s\n", hit, r.URL.Path)
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte("blocked"))
		},
	})(echo()))
	defer evadeSrv.Close()
	fmt.Println("== 反绕过(b*a*d*w*o*r*d) ==")
	code, _ = post(evadeSrv, `{"msg":"say b*a*d*w*o*r*d"}`)
	fmt.Printf("b*a*d*w*o*r*d → %d (仍被识破)\n", code)
}
