// Package hls 提供一个直播/点播 HLS origin:管理滚动分片窗口、生成 m3u8 播放列表,
// 并作为 http.Handler 挂在任意 mux(如 pkg/service/webserver)上分发。
//
// 边界(和框架"薄机制"一致):本包**不做编解码/转码/切片**。分片(.ts / .m4s)由上游
// 产出后 Append 进来——上游可以是 ffmpeg,或 pkg/media/rtmp 采集后的 remux。纯 Go、
// 零 cgo,只负责"存分片窗口 + 出播放列表 + HTTP 分发"这三件苦活。
//
// 用法:
//
//	s := hls.NewStream(hls.WithWindow(6), hls.WithTargetDuration(2*time.Second))
//	mux.Handle("/live/", http.StripPrefix("/live", s)) // s 实现 http.Handler
//	// 上游每产出一个分片:
//	s.Append(segBytes, 1980*time.Millisecond)
//	// 直播结束转点播:
//	s.Finish()
//
// 播放端拉 /live/index.m3u8 → 得到 media playlist,再按其中的 seg{N}.ts 拉分片。
package hls

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// segMeta 是播放列表里一个分片的元数据(字节存 Store,不在此)。
type segMeta struct {
	Seq      uint64
	Duration time.Duration
}

type config struct {
	window    int
	targetDur time.Duration
	ext       string
	store     Store
}

// Option 配置 Stream。
type Option func(*config)

// WithWindow 设置直播播放列表保留的分片数(滚动窗口,默认 6)。点播不受此限。
func WithWindow(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.window = n
		}
	}
}

// WithTargetDuration 设置 #EXT-X-TARGETDURATION(默认从分片时长推断,取上界并向上取整)。
func WithTargetDuration(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.targetDur = d
		}
	}
}

// WithSegmentExt 设置分片扩展名(默认 ".ts";fMP4 用 ".m4s",此时通常需配 SetInitSegment)。
func WithSegmentExt(ext string) Option {
	return func(c *config) {
		if ext != "" {
			c.ext = ext
		}
	}
}

// WithStore 设置分片存储后端(默认内存)。可用 NewDiskStore 落磁盘,或自实现对象存储。
func WithStore(s Store) Option {
	return func(c *config) {
		if s != nil {
			c.store = s
		}
	}
}

// Stream 是一路 HLS 流。零值不可用,用 NewStream 构造。并发安全。
type Stream struct {
	cfg config

	mu       sync.RWMutex
	segments []segMeta // 直播:滚动窗口;点播:全量(元数据;字节在 Store)
	mediaSeq uint64    // 窗口内首个分片的序号(#EXT-X-MEDIA-SEQUENCE)
	nextSeq  uint64
	initSeg  []byte // fMP4 init segment(#EXT-X-MAP),可选
	finished bool   // Finish 后为 true(点播:加 #EXT-X-ENDLIST、不再淘汰)
}

// NewStream 创建一路 HLS 流。
func NewStream(opts ...Option) *Stream {
	s := &Stream{cfg: config{window: 6, ext: ".ts"}}
	for _, o := range opts {
		o(&s.cfg)
	}
	if s.cfg.store == nil {
		s.cfg.store = NewMemoryStore()
	}
	return s
}

// SetInitSegment 设置 fMP4 的初始化分片(EXT-X-MAP,URI 为 init.mp4)。TS 分片无需调用。
func (s *Stream) SetInitSegment(data []byte) {
	s.mu.Lock()
	s.initSeg = data
	s.mu.Unlock()
}

// Append 追加一个分片,返回其序号。分片字节写入 Store;直播模式下超出窗口会淘汰最旧
// 分片(从 Store 删除并推进 media-sequence)。Finish 之后调用无效果,返回 (0, nil)。
// Store 写入失败时返回错误(此时不改动播放列表)。
func (s *Stream) Append(data []byte, dur time.Duration) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		return 0, nil
	}
	seq := s.nextSeq
	if err := s.cfg.store.Put(seq, data); err != nil {
		return 0, err
	}
	s.nextSeq++
	s.segments = append(s.segments, segMeta{Seq: seq, Duration: dur})
	// 直播滚动窗口:超窗淘汰最旧(从 Store 删除,best-effort)。
	for len(s.segments) > s.cfg.window {
		s.cfg.store.Remove(s.segments[0].Seq)
		s.segments = s.segments[1:]
		s.mediaSeq++
	}
	return seq, nil
}

// Finish 标记流结束:播放列表加 #EXT-X-ENDLIST 变为点播,且不再淘汰当前窗口内分片。
func (s *Stream) Finish() {
	s.mu.Lock()
	s.finished = true
	s.mu.Unlock()
}

// SegmentData 按序号取分片数据(仍在窗口内才有)。
func (s *Stream) SegmentData(seq uint64) ([]byte, bool) {
	data, ok, _ := s.cfg.store.Get(seq)
	return data, ok
}

// targetDurationSec 计算 TARGETDURATION(秒,整数,须 ≥ 最长分片)。
func (s *Stream) targetDurationSec() int {
	if s.cfg.targetDur > 0 {
		return int(math.Ceil(s.cfg.targetDur.Seconds()))
	}
	var maxDur time.Duration
	for i := range s.segments {
		if s.segments[i].Duration > maxDur {
			maxDur = s.segments[i].Duration
		}
	}
	if maxDur <= 0 {
		return 1
	}
	return int(math.Ceil(maxDur.Seconds()))
}

// MediaPlaylist 生成当前的 media playlist(m3u8)。
func (s *Stream) MediaPlaylist() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var b strings.Builder
	version := 3
	if s.initSeg != nil {
		version = 7 // fMP4 需要 EXT-X-MAP,提升版本
	}
	fmt.Fprintf(&b, "#EXTM3U\n#EXT-X-VERSION:%d\n", version)
	fmt.Fprintf(&b, "#EXT-X-TARGETDURATION:%d\n", s.targetDurationSec())
	fmt.Fprintf(&b, "#EXT-X-MEDIA-SEQUENCE:%d\n", s.mediaSeq)
	if !s.finished {
		b.WriteString("#EXT-X-PLAYLIST-TYPE:EVENT\n")
	} else {
		b.WriteString("#EXT-X-PLAYLIST-TYPE:VOD\n")
	}
	if s.initSeg != nil {
		b.WriteString("#EXT-X-MAP:URI=\"init.mp4\"\n")
	}
	for i := range s.segments {
		fmt.Fprintf(&b, "#EXTINF:%.3f,\n", s.segments[i].Duration.Seconds())
		fmt.Fprintf(&b, "seg%d%s\n", s.segments[i].Seq, s.cfg.ext)
	}
	if s.finished {
		b.WriteString("#EXT-X-ENDLIST\n")
	}
	return []byte(b.String())
}

// ServeHTTP 实现 http.Handler:分发播放列表、init 分片与各媒体分片。
// 路由(去掉挂载前缀后):
//   - *.m3u8      → media playlist
//   - init.mp4    → fMP4 初始化分片(若已 SetInitSegment)
//   - seg{N}.ext  → 序号 N 的分片
func (s *Stream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	name := strings.TrimPrefix(r.URL.Path, "/")
	// 取最后一段,兼容各种挂载前缀。
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}

	switch {
	case strings.HasSuffix(name, ".m3u8"):
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(s.MediaPlaylist())

	case name == "init.mp4":
		s.mu.RLock()
		init := s.initSeg
		s.mu.RUnlock()
		if init == nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "video/mp4")
		w.Write(init)

	case strings.HasPrefix(name, "seg"):
		seq, ok := parseSegName(name, s.cfg.ext)
		if !ok {
			http.NotFound(w, r)
			return
		}
		data, found := s.SegmentData(seq)
		if !found {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", segmentContentType(s.cfg.ext))
		w.Header().Set("Cache-Control", "max-age=31536000, immutable")
		w.Write(data)

	default:
		http.NotFound(w, r)
	}
}

// parseSegName 从 "seg{N}.ext" 解析序号。
func parseSegName(name, ext string) (uint64, bool) {
	s := strings.TrimPrefix(name, "seg")
	s = strings.TrimSuffix(s, ext)
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

func segmentContentType(ext string) string {
	switch ext {
	case ".m4s", ".mp4":
		return "video/mp4"
	default:
		return "video/mp2t" // .ts
	}
}
