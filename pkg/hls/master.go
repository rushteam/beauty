package hls

import (
	"fmt"
	"net/http"
	"strings"
)

// Variant 是 ABR(自适应码率)里的一路码率变体。Handler 负责分发这一路的 media
// playlist 与分片——既可是本包的 *Stream(进程内 remux/copy),也可是 http.FileServer
// (指向 ffmpeg 转码写出的目录),因此 Master 能同时适配"进程内"与"ffmpeg 外部转码"两种产源。
type Variant struct {
	Name       string       // URL 路径段,如 "720p"(master 里引用为 720p/index.m3u8)
	Bandwidth  int          // 峰值码率 bits/s(BANDWIDTH,必填)
	Resolution string       // 如 "1280x720"(RESOLUTION,可空)
	Codecs     string       // 如 "avc1.64001f,mp4a.40.2"(CODECS,可空)
	Playlist   string       // 变体 media playlist 的文件名(默认 "index.m3u8")
	Handler    http.Handler // 分发该变体 media playlist + 分片
}

// Master 把多路码率变体组合成一个 ABR 主清单,并按变体名路由到各自 Handler。
type Master struct {
	variants []Variant
}

// NewMaster 创建主清单。各 Variant 至少要有 Name、Bandwidth、Handler。
func NewMaster(variants ...Variant) *Master {
	return &Master{variants: variants}
}

func (v Variant) playlistName() string {
	if v.Playlist != "" {
		return v.Playlist
	}
	return "index.m3u8"
}

// Playlist 生成 master playlist(m3u8)。
func (m *Master) Playlist() []byte {
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:6\n#EXT-X-INDEPENDENT-SEGMENTS\n")
	for _, v := range m.variants {
		fmt.Fprintf(&b, "#EXT-X-STREAM-INF:BANDWIDTH=%d", v.Bandwidth)
		if v.Resolution != "" {
			fmt.Fprintf(&b, ",RESOLUTION=%s", v.Resolution)
		}
		if v.Codecs != "" {
			fmt.Fprintf(&b, ",CODECS=%q", v.Codecs)
		}
		b.WriteByte('\n')
		fmt.Fprintf(&b, "%s/%s\n", v.Name, v.playlistName())
	}
	return []byte(b.String())
}

// ServeHTTP 实现 http.Handler:根路径的 *.m3u8 返回 master playlist;/{variant}/… 去掉
// 变体名前缀后交给该变体的 Handler。
func (m *Master) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	p := strings.TrimPrefix(r.URL.Path, "/")

	// 根 .m3u8 → master
	if !strings.Contains(p, "/") && strings.HasSuffix(p, ".m3u8") {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(m.Playlist())
		return
	}

	name, rest, ok := strings.Cut(p, "/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	for _, v := range m.variants {
		if v.Name == name {
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/" + rest
			v.Handler.ServeHTTP(w, r2)
			return
		}
	}
	http.NotFound(w, r)
}
