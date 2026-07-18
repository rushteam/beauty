package hlsmux

import (
	"bytes"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ---- 纯函数单测 ----

func TestSigned24(t *testing.T) {
	cases := []struct {
		a, b, c byte
		want    int32
	}{
		{0, 0, 0, 0},
		{0, 0, 1, 1},
		{0x00, 0x01, 0x00, 256},
		{0xFF, 0xFF, 0xFF, -1},       // 全 1 → -1
		{0x80, 0x00, 0x00, -8388608}, // 最小负数
	}
	for _, c := range cases {
		if got := signed24(c.a, c.b, c.c); got != c.want {
			t.Errorf("signed24(%#x,%#x,%#x) = %d, want %d", c.a, c.b, c.c, got, c.want)
		}
	}
}

func TestSplitAVCC(t *testing.T) {
	// 两个 NALU:[len=3]{1,2,3} [len=2]{4,5}
	var buf bytes.Buffer
	writeAVCC(&buf, []byte{1, 2, 3})
	writeAVCC(&buf, []byte{4, 5})
	got := splitAVCC(buf.Bytes())
	if len(got) != 2 {
		t.Fatalf("got %d NALUs, want 2", len(got))
	}
	if !bytes.Equal(got[0], []byte{1, 2, 3}) || !bytes.Equal(got[1], []byte{4, 5}) {
		t.Fatalf("NALUs = %v, want [[1 2 3] [4 5]]", got)
	}
	// 截断的长度前缀应被安全丢弃,不 panic。
	if out := splitAVCC([]byte{0, 0, 0, 10, 1, 2}); len(out) != 0 {
		t.Fatalf("truncated NALU should yield 0, got %v", out)
	}
}

func TestFirstSPSPPS(t *testing.T) {
	rec := buildAVCDecoderConfig(testSPS, testPPS)
	sps, pps, ok := firstSPSPPS(rec)
	if !ok {
		t.Fatal("firstSPSPPS 未解析成功")
	}
	if !bytes.Equal(sps, testSPS) {
		t.Errorf("SPS = %#x, want %#x", sps, testSPS)
	}
	if !bytes.Equal(pps, testPPS) {
		t.Errorf("PPS = %#x, want %#x", pps, testPPS)
	}
	if _, _, ok := firstSPSPPS([]byte{1, 2}); ok {
		t.Error("过短的 record 应解析失败")
	}
}

// ---- 集成:喂真实 SPS/PPS/ASC + 帧,验证 gohlslib 起 muxer 并出播放列表 ----

// feed 把序列头 + n 帧(每 4 帧一个关键帧,间隔 40ms)喂给 bridge。
func feed(t *testing.T, b *Bridge, n int) {
	t.Helper()
	if err := b.OnVideo(0, videoSeqHeader(testSPS, testPPS)); err != nil {
		t.Fatalf("OnVideo seq: %v", err)
	}
	if err := b.OnAudio(0, audioSeqHeader(testASC)); err != nil {
		t.Fatalf("OnAudio seq: %v", err)
	}
	ts := uint32(0)
	for i := range n {
		key := i%4 == 0
		nal := interNAL
		if key {
			nal = idrNAL
		}
		if err := b.OnVideo(ts, videoFrame(key, nal)); err != nil {
			t.Fatalf("OnVideo frame %d: %v", i, err)
		}
		if err := b.OnAudio(ts, audioFrame([]byte{0x01, 0x02, 0x03, 0x04})); err != nil {
			t.Fatalf("OnAudio frame %d: %v", i, err)
		}
		ts += 40
	}
}

func TestBridge_ServeAfterKeyframe_MPEGTS(t *testing.T) {
	b := NewBridge(
		WithVariant(VariantMPEGTS), // MPEG-TS:最确定,便于断言
		WithSegmentMinDuration(100*time.Millisecond),
		WithSegmentCount(5),
	)
	// 起流前:503。
	if rec := serve(t, b, "/index.m3u8"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("起流前 status = %d, want 503", rec.Code)
	}
	feed(t, b, 16) // 跨越多个分片边界

	rec := serve(t, b, "/index.m3u8")
	if rec.Code != http.StatusOK {
		t.Fatalf("起流后 status = %d, want 200; body=%q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "#EXTM3U") {
		t.Fatalf("播放列表缺少 #EXTM3U:%q", rec.Body.String())
	}
	b.OnClose() // 不应 panic
}

func TestBridge_LowLatency(t *testing.T) {
	b := NewBridge( // 默认 LL-HLS
		WithSegmentMinDuration(100*time.Millisecond),
		WithPartMinDuration(20*time.Millisecond),
	)
	feed(t, b, 24) // 喂够帧,确保有已就绪的分片(否则 LL-HLS 主清单会阻塞等待)

	rec := serve(t, b, "/index.m3u8")
	if rec.Code != http.StatusOK {
		t.Fatalf("LL-HLS status = %d, want 200; body=%q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "#EXTM3U") {
		t.Fatalf("LL-HLS 播放列表缺少 #EXTM3U:%q", rec.Body.String())
	}
	b.OnClose()
}

// ---- 测试辅助 ----

// serve 带超时护栏:LL-HLS 未就绪时 Handle 可能阻塞,超时即判失败而非挂住整个测试。
func serve(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("ServeHTTP(%s) 超时", path)
	}
	return rec
}

func writeAVCC(w *bytes.Buffer, nal []byte) {
	var l [4]byte
	binary.BigEndian.PutUint32(l[:], uint32(len(nal)))
	w.Write(l[:])
	w.Write(nal)
}

// 真实的 H.264 high-profile SPS/PPS(常见测试向量)。
var (
	testSPS = []byte{0x67, 0x64, 0x00, 0x0a, 0xac, 0xd9, 0x44, 0x26, 0x84, 0x00, 0x00,
		0x03, 0x00, 0x04, 0x00, 0x00, 0x03, 0x00, 0xca, 0x3c, 0x48, 0x96, 0x11, 0x80}
	testPPS = []byte{0x68, 0xe8, 0x43, 0x8f, 0x13, 0x21, 0x30}
	// AAC-LC 48kHz 立体声。
	testASC = []byte{0x11, 0x90}
	// 最小 NALU:IDR(type 5)与非 IDR(type 1)。
	idrNAL   = []byte{0x65, 0x88, 0x84, 0x00, 0x21, 0xff}
	interNAL = []byte{0x41, 0x9a, 0x00, 0x10}
)

// buildAVCDecoderConfig 构造一个含单组 SPS/PPS 的 AVCDecoderConfigurationRecord。
func buildAVCDecoderConfig(sps, pps []byte) []byte {
	b := []byte{1, sps[1], sps[2], sps[3], 0xFF, 0xE0 | 1}
	b = append(b, byte(len(sps)>>8), byte(len(sps)))
	b = append(b, sps...)
	b = append(b, 1) // numPPS
	b = append(b, byte(len(pps)>>8), byte(len(pps)))
	b = append(b, pps...)
	return b
}

// videoSeqHeader 构造 FLV 视频 tag body:AVC 序列头。
func videoSeqHeader(sps, pps []byte) []byte {
	return append([]byte{0x17, 0x00, 0x00, 0x00, 0x00}, buildAVCDecoderConfig(sps, pps)...)
}

// videoFrame 构造 FLV 视频 tag body:AVC NALU 帧(AVCC)。
func videoFrame(key bool, nal []byte) []byte {
	b0 := byte(0x27) // 非关键帧 + AVC
	if key {
		b0 = 0x17
	}
	out := []byte{b0, 0x01, 0x00, 0x00, 0x00}
	var buf bytes.Buffer
	writeAVCC(&buf, nal)
	return append(out, buf.Bytes()...)
}

// audioSeqHeader 构造 FLV 音频 tag body:AAC 序列头(AudioSpecificConfig)。
func audioSeqHeader(asc []byte) []byte {
	return append([]byte{0xAF, 0x00}, asc...)
}

// audioFrame 构造 FLV 音频 tag body:裸 AAC 帧。
func audioFrame(au []byte) []byte {
	return append([]byte{0xAF, 0x01}, au...)
}
