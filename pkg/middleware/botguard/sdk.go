package botguard

import (
	_ "embed"
	"net/http"
)

//go:embed botguard.js
var sdkJS []byte

// SDKSource 返回浏览器 SDK 源码，便于接入构建流水线做混淆 / 虚拟化后再发布。
func SDKSource() []byte { return sdkJS }

// SDKHandler 直接托管未混淆的浏览器 SDK，适合开发联调；生产建议发布处理过的版本。
func SDKHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=300")
		_, _ = w.Write(sdkJS)
	})
}
