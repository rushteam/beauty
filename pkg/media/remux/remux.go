// Package remux 把 RTMP 收到的 FLV(H.264 + AAC)重封装成 MPEG-TS 分片,按关键帧切片
// 后 Append 到 pkg/hls.Stream——从而把 pkg/media/rtmp(采集)和 pkg/hls(分发)串成
// 一条端到端直播链路。**不转码**:只做容器层重封装(FLV→TS)与 H.264 AVCC→AnnexB、
// AAC→ADTS 的格式转换,编解码原样透传。TS 打包用纯 Go 的 github.com/asticode/go-astits。
//
// 用法(接在 rtmp.Server 后面):
//
//	stream := hls.NewStream(...)
//	srv := rtmp.NewServer(":1935", func(key string) rtmp.Handler {
//	    return remux.NewFLVToHLS(stream)     // FLVToHLS 实现 rtmp.Handler
//	})
//	// stream 挂在 webserver 上分发(见 examples/live-hls)。
//
// 限制(如实标注):
//   - 只支持 H.264(AVC)视频 + AAC 音频——OBS/ffmpeg 默认即此组合;其它编码丢弃。
//   - 在视频关键帧(IDR)处切片,故分片时长 ≈ max(WithSegmentDuration, GOP 长度);
//     推流端 keyframe interval 决定实际下限。
//   - 单元测试覆盖 FLV 解析/AVCC→AnnexB/ADTS/切片机制并校验产出合法 TS(0x47 同步字);
//     但**真机播放器(Safari/hls.js/ffplay)互操作未在此验证**,生产前请用真实流校验。
package remux

import (
	"bytes"
	"context"
	"encoding/binary"
	"time"

	"github.com/asticode/go-astits"

	"github.com/rushteam/beauty/pkg/hls"
)

const (
	videoPID = 256
	audioPID = 257
)

// FLVToHLS 实现 pkg/media/rtmp.Handler:把一路 FLV 流重封装为 TS 分片写入 hls.Stream。
// 方法由 go-rtmp 在单个连接 goroutine 内串行调用,故内部状态无需加锁。
type FLVToHLS struct {
	out    *hls.Stream
	segDur time.Duration

	// 编解码配置(从 seq header 得到)
	sps, pps       [][]byte
	haveVideo      bool
	aacOT, aacFreq byte
	aacCh          byte
	haveAudio      bool

	// 当前分片
	buf        *bytes.Buffer
	mux        *astits.Muxer
	segStartMS int64
	lastMS     int64
	open       bool
}

// Option 配置 FLVToHLS。
type Option func(*FLVToHLS)

// WithSegmentDuration 设置目标分片时长(默认 2s)。实际按关键帧对齐,故不小于 GOP 长度。
func WithSegmentDuration(d time.Duration) Option {
	return func(r *FLVToHLS) {
		if d > 0 {
			r.segDur = d
		}
	}
}

// NewFLVToHLS 创建一个把 FLV 重封装到 out 的 remuxer。
func NewFLVToHLS(out *hls.Stream, opts ...Option) *FLVToHLS {
	r := &FLVToHLS{out: out, segDur: 2 * time.Second}
	for _, o := range opts {
		o(r)
	}
	return r
}

// OnMetaData 实现 rtmp.Handler(本 remuxer 不需要 onMetaData,忽略)。
func (r *FLVToHLS) OnMetaData([]byte) {}

// OnVideo 实现 rtmp.Handler:解析 FLV 视频 tag,关键帧切片并写 TS。
func (r *FLVToHLS) OnVideo(ts uint32, data []byte) error {
	if len(data) < 5 {
		return nil
	}
	frameType := data[0] >> 4
	codecID := data[0] & 0x0F
	if codecID != 7 { // 只支持 AVC/H.264
		return nil
	}
	pktType := data[1]
	// composition time:有符号 24-bit
	cts := int32(data[2])<<16 | int32(data[3])<<8 | int32(data[4])
	if cts&0x800000 != 0 {
		cts |= ^int32(0xFFFFFF)
	}
	payload := data[5:]
	r.lastMS = int64(ts)

	switch pktType {
	case 0: // AVCDecoderConfigurationRecord
		if sps, pps, ok := parseAVCC(payload); ok {
			r.sps, r.pps, r.haveVideo = sps, pps, true
		}
		return nil
	case 1: // NALU
		key := frameType == 1
		tsMS := int64(ts)
		if key && r.open && tsMS-r.segStartMS >= r.segDur.Milliseconds() {
			if err := r.finalize(tsMS); err != nil {
				return err
			}
		}
		if key && !r.open {
			if err := r.startSegment(tsMS); err != nil {
				return err
			}
		}
		if !r.open { // 还没等到第一个关键帧,丢弃
			return nil
		}
		var au []byte
		if key { // 关键帧前置 SPS/PPS,保证分片自解码
			for _, s := range r.sps {
				au = appendAnnexB(au, s)
			}
			for _, p := range r.pps {
				au = appendAnnexB(au, p)
			}
		}
		au = avccNALs(payload, au)
		return r.writeVideo(au, tsMS, int64(cts))
	}
	return nil
}

// OnAudio 实现 rtmp.Handler:解析 FLV 音频 tag,AAC 加 ADTS 头后写 TS。
func (r *FLVToHLS) OnAudio(ts uint32, data []byte) error {
	if len(data) < 2 {
		return nil
	}
	if data[0]>>4 != 10 { // 只支持 AAC
		return nil
	}
	pktType := data[1]
	payload := data[2:]
	r.lastMS = int64(ts)

	if pktType == 0 { // AudioSpecificConfig
		if ot, fi, ch, ok := parseASC(payload); ok {
			r.aacOT, r.aacFreq, r.aacCh, r.haveAudio = ot, fi, ch, true
		}
		return nil
	}
	if !r.open || !r.haveAudio {
		return nil
	}
	frame := append(adtsHeader(r.aacOT, r.aacFreq, r.aacCh, len(payload)), payload...)
	_, err := r.mux.WriteData(&astits.MuxerData{
		PID: audioPID,
		PES: &astits.PESData{
			Header: &astits.PESHeader{
				StreamID: 0xC0,
				OptionalHeader: &astits.PESOptionalHeader{
					MarkerBits:             2,
					PTSDTSIndicator:        astits.PTSDTSIndicatorOnlyPTS,
					DataAlignmentIndicator: true,
					PTS:                    &astits.ClockReference{Base: int64(ts) * 90},
				},
			},
			Data: frame,
		},
	})
	return err
}

// OnClose 实现 rtmp.Handler:收尾最后一个分片并把流封为点播。
func (r *FLVToHLS) OnClose() {
	_ = r.finalize(r.lastMS)
	r.out.Finish()
}

func (r *FLVToHLS) writeVideo(au []byte, tsMS, ctsMS int64) error {
	_, err := r.mux.WriteData(&astits.MuxerData{
		PID: videoPID,
		PES: &astits.PESData{
			Header: &astits.PESHeader{
				StreamID: 0xE0,
				OptionalHeader: &astits.PESOptionalHeader{
					MarkerBits:             2,
					PTSDTSIndicator:        astits.PTSDTSIndicatorBothPresent,
					DataAlignmentIndicator: true,
					PTS:                    &astits.ClockReference{Base: (tsMS + ctsMS) * 90},
					DTS:                    &astits.ClockReference{Base: tsMS * 90},
				},
			},
			Data: au,
		},
	})
	return err
}

func (r *FLVToHLS) startSegment(tsMS int64) error {
	r.buf = &bytes.Buffer{}
	r.mux = astits.NewMuxer(context.Background(), r.buf)
	if err := r.mux.AddElementaryStream(astits.PMTElementaryStream{ElementaryPID: videoPID, StreamType: astits.StreamTypeH264Video}); err != nil {
		return err
	}
	if err := r.mux.AddElementaryStream(astits.PMTElementaryStream{ElementaryPID: audioPID, StreamType: astits.StreamTypeAACAudio}); err != nil {
		return err
	}
	r.mux.SetPCRPID(videoPID)
	if _, err := r.mux.WriteTables(); err != nil { // 每个分片开头写 PAT/PMT,保证独立可解码
		return err
	}
	r.segStartMS = tsMS
	r.open = true
	return nil
}

func (r *FLVToHLS) finalize(endMS int64) error {
	if !r.open {
		return nil
	}
	dur := time.Duration(endMS-r.segStartMS) * time.Millisecond
	if dur <= 0 {
		dur = r.segDur
	}
	_, err := r.out.Append(r.buf.Bytes(), dur)
	r.open, r.mux, r.buf = false, nil, nil
	return err
}

// ===== FLV / H.264 / AAC 格式转换(纯函数,便于单测)=====

var annexBStartCode = []byte{0, 0, 0, 1}

func appendAnnexB(dst, nal []byte) []byte {
	dst = append(dst, annexBStartCode...)
	return append(dst, nal...)
}

// avccNALs 把 AVCC(4 字节长度前缀的 NALU 序列)转为 AnnexB,追加到 dst。
func avccNALs(b, dst []byte) []byte {
	for len(b) >= 4 {
		n := int(binary.BigEndian.Uint32(b[:4]))
		b = b[4:]
		if n > len(b) {
			break
		}
		dst = appendAnnexB(dst, b[:n])
		b = b[n:]
	}
	return dst
}

// parseAVCC 从 AVCDecoderConfigurationRecord 解析 SPS/PPS。
func parseAVCC(b []byte) (sps, pps [][]byte, ok bool) {
	if len(b) < 6 {
		return nil, nil, false
	}
	off := 5
	numSPS := int(b[off] & 0x1F)
	off++
	for range numSPS {
		if off+2 > len(b) {
			return nil, nil, false
		}
		l := int(b[off])<<8 | int(b[off+1])
		off += 2
		if off+l > len(b) {
			return nil, nil, false
		}
		sps = append(sps, b[off:off+l])
		off += l
	}
	if off >= len(b) {
		return nil, nil, false
	}
	numPPS := int(b[off])
	off++
	for range numPPS {
		if off+2 > len(b) {
			return nil, nil, false
		}
		l := int(b[off])<<8 | int(b[off+1])
		off += 2
		if off+l > len(b) {
			return nil, nil, false
		}
		pps = append(pps, b[off:off+l])
		off += l
	}
	return sps, pps, true
}

// parseASC 从 AudioSpecificConfig 解析 objectType / 采样率索引 / 声道数。
func parseASC(b []byte) (objectType, freqIdx, channels byte, ok bool) {
	if len(b) < 2 {
		return 0, 0, 0, false
	}
	objectType = (b[0] >> 3) & 0x1F
	freqIdx = ((b[0] & 0x07) << 1) | (b[1] >> 7)
	channels = (b[1] >> 3) & 0x0F
	return objectType, freqIdx, channels, true
}

// adtsHeader 生成 7 字节 ADTS 头(无 CRC),把裸 AAC 帧包成可在 TS 里传输的 ADTS。
func adtsHeader(objectType, freqIdx, channels byte, payloadLen int) []byte {
	frameLen := 7 + payloadLen
	profile := objectType - 1 // ADTS profile = MPEG-4 audio object type - 1
	h := make([]byte, 7)
	h[0] = 0xFF
	h[1] = 0xF1 // syncword 低 4 位 + MPEG-4 + layer0 + protection absent
	h[2] = (profile << 6) | (freqIdx << 2) | ((channels >> 2) & 0x01)
	h[3] = (channels&0x03)<<6 | byte((frameLen>>11)&0x03)
	h[4] = byte((frameLen >> 3) & 0xFF)
	h[5] = byte((frameLen&0x07)<<5) | 0x1F
	h[6] = 0xFC
	return h
}
