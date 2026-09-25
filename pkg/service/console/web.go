package console

import (
	_ "embed"
	"html/template"
	"net/http"
	"sync"
)

//go:embed index.html
var indexHTML string

var (
	tmplOnce sync.Once
	tmpl     *template.Template
	tmplErr  error
)

// pageData 注入到前端页面的配置。
type pageData struct {
	Title string
	WSURL string // 相对路径,前端据当前 location 拼出 ws/wss 绝对地址
}

// handlePage 返回控制台 HTML 页面。
func (s *Server) handlePage(w http.ResponseWriter, r *http.Request) {
	tmplOnce.Do(func() {
		tmpl, tmplErr = template.New("console").Parse(indexHTML)
	})
	if tmplErr != nil {
		http.Error(w, tmplErr.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = tmpl.Execute(w, pageData{
		Title: s.title,
		WSURL: s.path + "/ws",
	})
}
