// Package hlsmux 把 RTMP 采集到的 FLV(H.264 + AAC)喂给 bluenviron/gohlslib,产出
// 生产级 HLS——支持 MPEG-TS、fMP4 和 **LL-HLS(低延迟)** 三种 variant。相比自研的
// pkg/media/remux(仅 FLV→TS、手搓播放列表),这里由 gohlslib 负责分片、播放列表(含
// LL-HLS EXT-X-PART/PRELOAD-HINT 与阻塞式刷新)、fMP4 init 段等全部 HLS 细节。
//
// 定位:这是 pkg/hls(自研播放列表 origin)+ pkg/media/remux(自研 FLV→TS)之外的
// **另一条 HLS 路径**——三者并存,按需选:
//   - 要最薄、零第三方 HLS 依赖、够用的直播:pkg/hls + pkg/media/remux;
//   - 要 LL-HLS/fMP4、经过 mediamtx 同款库打磨的产线级 HLS:本包(gohlslib)。
//
// 用法(接在 rtmp.Server 后面,Bridge 同时是 rtmp.Handler 与 http.Handler):
//
//	b := hlsmux.NewBridge(hlsmux.WithVariant(hlsmux.VariantLowLatency))
//	srv := rtmp.NewServer(":1935", func(key string) rtmp.Handler { return b })
//	mux.Handle("/hls/", http.StripPrefix("/hls", b)) // 播放:/hls/index.m3u8
//
// 边界(机制而非策略):只做「FLV 解封装 + 交给 gohlslib」。分片时长、LL-HLS 参数、
// 存盘目录是 gohlslib 的配置(经 Option 透出);多路流管理用 pkg/media.Hub;转码、鉴权、
// 多码率仍在框架之外。
//
// 限制(如实标注):只桥接 H.264(AVC)视频 + AAC 音频(OBS/ffmpeg 默认组合),其它
// 编码丢弃;真机播放器互操作请用真实流校验(单测用 gohlslib 自身校验产出结构)。
package hlsmux

import (
	"encoding/binary"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	gohlslib "github.com/bluenviron/gohlslib/v2"
	"github.com/bluenviron/gohlslib/v2/pkg/codecs"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/mpeg4audio"

	"github.com/rushteam/beauty/pkg/media/rtmp"
)

// Variant 选择 HLS 封装形态。
type Variant = gohlslib.MuxerVariant

// HLS variant:MPEG-TS(最兼容)、fMP4(点播/字节范围友好)、LowLatency(LL-HLS,默认)。
const (
	VariantMPEGTS     = gohlslib.MuxerVariantMPEGTS
	VariantFMP4       = gohlslib.MuxerVariantFMP4
	VariantLowLatency = gohlslib.MuxerVariantLowLatency
)

// Bridge 把一路 FLV 流桥接到 gohlslib.Muxer。实现 rtmp.Handler(收流)与 http.Handler
// (播放)。方法由 rtmp 在单连接 goroutine 内串行调用;ServeHTTP 可与之并发(gohlslib
// 的 Handle 并发安全)。零值不可用,用 NewBridge 构造;一个 Bridge 只服务一路流。
type Bridge struct {
	variant    Variant
	segCount   int
	segMinDur  time.Duration
	partMinDur time.Duration
	dir        string

	mu       sync.Mutex
	started  bool
	sps, pps []byte
	haveASC  bool
	asc      mpeg4audio.AudioSpecificConfig
	vtrack   *gohlslib.Track
	atrack   *gohlslib.Track
	m        *gohlslib.Muxer

	handle atomic.Pointer[gohlslib.Muxer] // start 后发布,供 ServeHTTP 无锁读取
}

// Option 配置 Bridge(透出 gohlslib.Muxer 的常用参数)。
type Option func(*Bridge)

// WithVariant 选择 HLS 形态(默认 LL-HLS)。
func WithVariant(v Variant) Option { return func(b *Bridge) { b.variant = v } }

// WithSegmentCount 设置服务端保留的分片数(默认 gohlslib 的 7)。
func WithSegmentCount(n int) Option {
	return func(b *Bridge) {
		if n > 0 {
			b.segCount = n
		}
	}
}

// WithSegmentMinDuration 设置分片最小时长(会按 IDR 对齐;默认 gohlslib 的 1s)。
func WithSegmentMinDuration(d time.Duration) Option {
	return func(b *Bridge) {
		if d > 0 {
			b.segMinDur = d
		}
	}
}

// WithPartMinDuration 设置 LL-HLS 部分分片最小时长(默认 gohlslib 的 200ms)。
func WithPartMinDuration(d time.Duration) Option {
	return func(b *Bridge) {
		if d > 0 {
			b.partMinDur = d
		}
	}
}

// WithDirectory 把分片/播放列表落盘到目录(可 offload 内存或产出自包含目录给 CDN;
// LL-HLS variant 不支持落盘)。默认全内存。
func WithDirectory(dir string) Option { return func(b *Bridge) { b.dir = dir } }

// NewBridge 创建一个 FLV→gohlslib 桥接器。
func NewBridge(opts ...Option) *Bridge {
	b := &Bridge{variant: VariantLowLatency}
	for _, o := range opts {
		o(b)
	}
	return b
}

var _ rtmp.Handler = (*Bridge)(nil)
var _ http.Handler = (*Bridge)(nil)

// OnMetaData 实现 rtmp.Handler(不需要,忽略)。
func (b *Bridge) OnMetaData([]byte) {}

// OnVideo 实现 rtmp.Handler:解析 FLV 视频 tag,喂 gohlslib。
func (b *Bridge) OnVideo(ts uint32, data []byte) error {
	if len(data) < 5 {
		return nil
	}
	frameType := data[0] >> 4
	if data[0]&0x0F != 7 { // 只支持 AVC/H.264
		return nil
	}
	pktType := data[1]
	cts := signed24(data[2], data[3], data[4]) // composition time offset(ms)
	payload := data[5:]

	switch pktType {
	case 0: // AVCDecoderConfigurationRecord
		if sps, pps, ok := firstSPSPPS(payload); ok {
			b.mu.Lock()
			b.sps, b.pps = sps, pps
			b.mu.Unlock()
		}
		return nil
	case 1: // NALU(AVCC)
		key := frameType == 1
		b.mu.Lock()
		if !b.started {
			if b.sps == nil || !key { // 等 seq header + 首个关键帧再起 muxer
				b.mu.Unlock()
				return nil
			}
			if err := b.start(); err != nil {
				b.mu.Unlock()
				return fmt.Errorf("hlsmux: start muxer: %w", err)
			}
		}
		m, vt, sps, pps := b.m, b.vtrack, b.sps, b.pps
		b.mu.Unlock()

		au := splitAVCC(payload)
		if len(au) == 0 {
			return nil
		}
		if key { // 关键帧前置 SPS/PPS:gohlslib 需带内 SPS 才能提取 DTS,也保证分片自解码
			au = append([][]byte{sps, pps}, au...)
		}
		pts := (int64(ts) + int64(cts)) * 90 // 90kHz
		if err := m.WriteH264(vt, time.Now(), pts, au); err != nil {
			return fmt.Errorf("hlsmux: write h264: %w", err)
		}
	}
	return nil
}

// OnAudio 实现 rtmp.Handler:解析 FLV 音频 tag,喂 gohlslib。
func (b *Bridge) OnAudio(ts uint32, data []byte) error {
	if len(data) < 2 {
		return nil
	}
	if data[0]>>4 != 10 { // 只支持 AAC
		return nil
	}
	pktType := data[1]
	payload := data[2:]

	if pktType == 0 { // AudioSpecificConfig
		var asc mpeg4audio.AudioSpecificConfig
		if err := asc.Unmarshal(payload); err == nil {
			b.mu.Lock()
			b.asc, b.haveASC = asc, true
			b.mu.Unlock()
		}
		return nil
	}

	b.mu.Lock()
	started, m, at := b.started, b.m, b.atrack
	b.mu.Unlock()
	if !started || at == nil {
		return nil // 音轨未建(还没起 muxer 或纯视频),丢弃
	}
	pts := int64(ts) * int64(at.ClockRate) / 1000
	if err := m.WriteMPEG4Audio(at, time.Now(), pts, [][]byte{payload}); err != nil {
		return fmt.Errorf("hlsmux: write aac: %w", err)
	}
	return nil
}

// OnClose 实现 rtmp.Handler:关闭 muxer。
func (b *Bridge) OnClose() {
	b.mu.Lock()
	m := b.m
	b.mu.Unlock()
	if m != nil {
		m.Close()
	}
}

// ServeHTTP 实现 http.Handler:把播放请求交给 gohlslib(未就绪前回 503)。
func (b *Bridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if m := b.handle.Load(); m != nil {
		m.Handle(w, r)
		return
	}
	http.Error(w, "hls stream not ready", http.StatusServiceUnavailable)
}

// start 在拿到编解码参数后构建并启动 gohlslib.Muxer。调用者须持有 b.mu。
func (b *Bridge) start() error {
	b.vtrack = &gohlslib.Track{
		Codec:     &codecs.H264{SPS: b.sps, PPS: b.pps},
		ClockRate: 90000,
	}
	tracks := []*gohlslib.Track{b.vtrack}
	if b.haveASC {
		b.atrack = &gohlslib.Track{
			Codec:     &codecs.MPEG4Audio{Config: b.asc},
			ClockRate: b.asc.SampleRate,
		}
		tracks = append(tracks, b.atrack)
	}
	m := &gohlslib.Muxer{
		Tracks:             tracks,
		Variant:            b.variant,
		SegmentCount:       b.segCount,
		SegmentMinDuration: b.segMinDur,
		PartMinDuration:    b.partMinDur,
		Directory:          b.dir,
	}
	if err := m.Start(); err != nil {
		return err
	}
	b.m = m
	b.started = true
	b.handle.Store(m)
	return nil
}

// ===== FLV / H.264 格式解析(纯函数,便于单测)=====

// signed24 把 3 字节大端解析为有符号 24-bit(FLV composition time)。
func signed24(a, b, c byte) int32 {
	v := int32(a)<<16 | int32(b)<<8 | int32(c)
	if v&0x800000 != 0 {
		v |= ^int32(0xFFFFFF)
	}
	return v
}

// splitAVCC 把 AVCC(4 字节长度前缀的 NALU 序列)拆成裸 NALU 列表(不含起始码)。
func splitAVCC(b []byte) [][]byte {
	var out [][]byte
	for len(b) >= 4 {
		n := int(binary.BigEndian.Uint32(b[:4]))
		b = b[4:]
		if n <= 0 || n > len(b) {
			break
		}
		out = append(out, b[:n])
		b = b[n:]
	}
	return out
}

// firstSPSPPS 从 AVCDecoderConfigurationRecord 取第一组 SPS/PPS(gohlslib codecs.H264 各取一条)。
func firstSPSPPS(rec []byte) (sps, pps []byte, ok bool) {
	if len(rec) < 6 {
		return nil, nil, false
	}
	off := 5
	numSPS := int(rec[off] & 0x1F)
	off++
	for range numSPS {
		if off+2 > len(rec) {
			return nil, nil, false
		}
		l := int(rec[off])<<8 | int(rec[off+1])
		off += 2
		if off+l > len(rec) {
			return nil, nil, false
		}
		if sps == nil {
			sps = rec[off : off+l]
		}
		off += l
	}
	if off >= len(rec) {
		return nil, nil, false
	}
	numPPS := int(rec[off])
	off++
	for range numPPS {
		if off+2 > len(rec) {
			return nil, nil, false
		}
		l := int(rec[off])<<8 | int(rec[off+1])
		off += 2
		if off+l > len(rec) {
			return nil, nil, false
		}
		if pps == nil {
			pps = rec[off : off+l]
		}
		off += l
	}
	return sps, pps, sps != nil && pps != nil
}
