package remux

import (
	"encoding/binary"
	"testing"

	"github.com/rushteam/beauty/pkg/hls"
)

func TestAVCCToAnnexB(t *testing.T) {
	nal1 := []byte{0x65, 0xAA, 0xBB}
	nal2 := []byte{0x41, 0xCC}
	var avcc []byte
	for _, n := range [][]byte{nal1, nal2} {
		var l [4]byte
		binary.BigEndian.PutUint32(l[:], uint32(len(n)))
		avcc = append(avcc, l[:]...)
		avcc = append(avcc, n...)
	}
	got := avccNALs(avcc, nil)
	want := append(append([]byte{0, 0, 0, 1}, nal1...), append([]byte{0, 0, 0, 1}, nal2...)...)
	if string(got) != string(want) {
		t.Fatalf("annexB = % x, want % x", got, want)
	}
}

func TestParseAVCC(t *testing.T) {
	sps := []byte{0x67, 0x42, 0x00, 0x1E}
	pps := []byte{0x68, 0xCE, 0x3C}
	b := []byte{1, sps[1], 0, sps[3], 0xFF, 0xE1}
	b = append(b, byte(len(sps)>>8), byte(len(sps)))
	b = append(b, sps...)
	b = append(b, 0x01) // numPPS
	b = append(b, byte(len(pps)>>8), byte(len(pps)))
	b = append(b, pps...)

	gotSPS, gotPPS, ok := parseAVCC(b)
	if !ok || len(gotSPS) != 1 || len(gotPPS) != 1 {
		t.Fatalf("parseAVCC ok=%v sps=%d pps=%d", ok, len(gotSPS), len(gotPPS))
	}
	if string(gotSPS[0]) != string(sps) || string(gotPPS[0]) != string(pps) {
		t.Fatalf("sps/pps mismatch: % x / % x", gotSPS[0], gotPPS[0])
	}
}

func TestParseASCAndADTS(t *testing.T) {
	// AAC-LC(objectType=2), freqIdx=4(44100), channels=2 → ASC {0x12,0x10}
	ot, fi, ch, ok := parseASC([]byte{0x12, 0x10})
	if !ok || ot != 2 || fi != 4 || ch != 2 {
		t.Fatalf("parseASC = ot=%d fi=%d ch=%d ok=%v; want 2/4/2", ot, fi, ch, ok)
	}
	h := adtsHeader(ot, fi, ch, 100)
	if len(h) != 7 {
		t.Fatalf("adts len = %d", len(h))
	}
	if h[0] != 0xFF || h[1]&0xF0 != 0xF0 {
		t.Fatalf("adts syncword bad: % x", h[:2])
	}
	// frame length = 7 + 100 = 107,写在 h[3](低2位)/h[4]/h[5](高3位)
	frameLen := (int(h[3]&0x03) << 11) | (int(h[4]) << 3) | (int(h[5]>>5) & 0x07)
	if frameLen != 107 {
		t.Fatalf("adts frameLen = %d, want 107", frameLen)
	}
	// profile = objectType-1 = 1
	if profile := h[2] >> 6; profile != 1 {
		t.Fatalf("adts profile = %d, want 1", profile)
	}
}

// TestRemuxProducesTSSegments 喂合成 FLV(H.264+AAC)包,验证在关键帧处切出合法 TS 分片。
func TestRemuxProducesTSSegments(t *testing.T) {
	stream := hls.NewStream(hls.WithWindow(10))
	r := NewFLVToHLS(stream)

	// --- 序列头 ---
	sps, pps := []byte{0x67, 0x42, 0x00, 0x1E}, []byte{0x68, 0xCE, 0x3C}
	cfg := []byte{1, 0x42, 0, 0x1E, 0xFF, 0xE1, byte(len(sps) >> 8), byte(len(sps))}
	cfg = append(cfg, sps...)
	cfg = append(cfg, 0x01, byte(len(pps)>>8), byte(len(pps)))
	cfg = append(cfg, pps...)
	if err := r.OnVideo(0, append([]byte{0x17, 0x00, 0, 0, 0}, cfg...)); err != nil {
		t.Fatalf("video seq header: %v", err)
	}
	if err := r.OnAudio(0, []byte{0xAF, 0x00, 0x12, 0x10}); err != nil {
		t.Fatalf("audio seq header: %v", err)
	}

	// 一个 NALU(AVCC:4字节长度 + 数据)
	nalu := func(b []byte) []byte {
		var l [4]byte
		binary.BigEndian.PutUint32(l[:], uint32(len(b)))
		return append(l[:], b...)
	}
	keyframe := append([]byte{0x17, 0x01, 0, 0, 0}, nalu([]byte{0x65, 0x88, 0x84, 0x00})...)
	interframe := append([]byte{0x27, 0x01, 0, 0, 0}, nalu([]byte{0x41, 0x9A, 0x00})...)
	aac := append([]byte{0xAF, 0x01}, make([]byte, 64)...) // 裸 AAC 帧(内容任意)

	// GOP1: t=0 关键帧 + 几帧
	r.OnVideo(0, keyframe)
	r.OnAudio(0, aac)
	r.OnVideo(1000, interframe)
	r.OnAudio(1000, aac)
	// GOP2: t=3000(>2s)关键帧 → 触发 GOP1 切片
	r.OnVideo(3000, keyframe)
	r.OnAudio(3000, aac)
	r.OnClose() // 收尾 GOP2 并封盘

	// 应至少切出 2 个分片(GOP1 + GOP2)。
	seg0, ok := stream.SegmentData(0)
	if !ok {
		t.Fatal("应产出 seg0")
	}
	if len(seg0) == 0 || len(seg0)%188 != 0 {
		t.Fatalf("TS 分片长度应为 188 的整数倍, got %d", len(seg0))
	}
	if seg0[0] != 0x47 {
		t.Fatalf("TS 分片应以同步字 0x47 开头, got 0x%02x", seg0[0])
	}
	if _, ok := stream.SegmentData(1); !ok {
		t.Fatal("应产出 seg1(GOP2)")
	}
}
