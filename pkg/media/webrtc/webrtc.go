// Package webrtc 提供 WebRTC 的 WHIP(采集)/WHEP(分发)薄机制,基于纯 Go 的
// pion/webrtc(零 cgo)。作为 pkg/hls(多秒级、面向 CDN 分发)之外,面向亚秒级、
// 交互式实时媒体的可选传输——和 pkg/quic + pkg/gameloop 同属「实时」家族。
//
// 延迟等级(选型要点):
//   - RTMP→HLS/LL-HLS(pkg/media/rtmp + pkg/hls):多秒级,一对多、可过 CDN,直播/点播分发。
//   - WebRTC WHIP/WHEP(本包):亚秒级、可双向,连麦/云游戏/实时协作。NAT 穿透靠 ICE/STUN/TURN。
//
// WHIP/WHEP 本质相同——都是「HTTP 一发一答的 SDP 协商,服务端做 answerer」:
//   - WHIP(Ingestion):推流端 POST 一个 offer,服务端收流。在回调里注册 pc.OnTrack 拿进来的轨道。
//   - WHEP(Egress):播放端 POST 一个 offer,服务端发流。在回调里 pc.AddTrack 把要发的轨道挂上。
//
// 方向由回调对 PeerConnection 做什么决定,机制是同一个(见 Endpoint / NewWHIP / NewWHEP)。
//
// 边界(机制而非策略):本包只负责 HTTP+SDP+ICE 协商、WHIP 资源生命周期(DELETE 拆除、
// 断连自动回收),以及 RTP 转发原语(Pipe——纯 RTP 包转发,不转码、不做抖动缓冲)。谁能推/
// 谁能看(鉴权)、转发拓扑(SFU/MCU)、编解码档位、STUN/TURN 选路、录制、CORS,全是 policy,
// 留给上层。Endpoint 是 http.Handler,用 beauty.WithWebServer 挂上即可,不自起监听(与 pkg/hls 一致)。
//
// 最小用法(把一路 WHIP 推流广播给多个 WHEP 播放端——最小 SFU):见 examples/webrtc-whip-whep。
package webrtc

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/pion/interceptor"
	pion "github.com/pion/webrtc/v4"
)

// NewAPI 构造一个 *webrtc.API:注册默认编解码集(Opus / VP8 / VP9 / H264 等)与默认
// 拦截器(NACK 重传、RTCP 收发报告、TWCC)。多数场景直接用它;需要自定义编解码时用
// WithMediaEngine 传入自行 RegisterCodec 过的 MediaEngine。
func NewAPI(opts ...APIOption) (*pion.API, error) {
	var cfg apiConfig
	for _, o := range opts {
		o(&cfg)
	}
	me := cfg.mediaEngine
	if me == nil {
		me = &pion.MediaEngine{}
		if err := me.RegisterDefaultCodecs(); err != nil {
			return nil, fmt.Errorf("webrtc: register default codecs: %w", err)
		}
	}
	ir := &interceptor.Registry{}
	if err := pion.RegisterDefaultInterceptors(me, ir); err != nil {
		return nil, fmt.Errorf("webrtc: register default interceptors: %w", err)
	}
	return pion.NewAPI(pion.WithMediaEngine(me), pion.WithInterceptorRegistry(ir)), nil
}

// APIOption 配置 NewAPI。
type APIOption func(*apiConfig)

type apiConfig struct {
	mediaEngine *pion.MediaEngine
}

// WithMediaEngine 用自定义 MediaEngine(自行 RegisterCodec / RegisterHeaderExtension)
// 替代默认编解码集——例如只保留 H264+Opus、或加私有 profile。
func WithMediaEngine(me *pion.MediaEngine) APIOption {
	return func(c *apiConfig) { c.mediaEngine = me }
}

// Answer 执行 WHIP/WHEP 服务端(answerer)一侧的协商:把远端 offer 应用到 pc、生成
// answer、等待 ICE 收集完成(WHIP/WHEP 只有一次 SDP 交换、不 trickle,故须等齐候选),
// 返回 answer 的 SDP。协商前应先在 pc 上注册 OnTrack(WHIP)或 AddTrack(WHEP)。
func Answer(pc *pion.PeerConnection, offer string) (string, error) {
	if err := pc.SetRemoteDescription(pion.SessionDescription{
		Type: pion.SDPTypeOffer,
		SDP:  offer,
	}); err != nil {
		return "", fmt.Errorf("webrtc: set remote offer: %w", err)
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return "", fmt.Errorf("webrtc: create answer: %w", err)
	}
	// GatheringCompletePromise 必须在 SetLocalDescription(触发 gather)之前拿到。
	gatherComplete := pion.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(answer); err != nil {
		return "", fmt.Errorf("webrtc: set local answer: %w", err)
	}
	<-gatherComplete
	return pc.LocalDescription().SDP, nil
}

// NewLocalTrackFor 按远端轨道的编解码能力建一个可写的本地轨道,用于把收到的 RTP
// 转发出去(转发拓扑的核心:一路 WHIP 进来的 remote track → 一个 local track → 多个
// WHEP 播放端 AddTrack 订阅)。id/streamID 是轨道标识(同一 streamID 下的音视频会被
// 播放端当作同一路媒体)。
func NewLocalTrackFor(remote *pion.TrackRemote, id, streamID string) (*pion.TrackLocalStaticRTP, error) {
	t, err := pion.NewTrackLocalStaticRTP(remote.Codec().RTPCodecCapability, id, streamID)
	if err != nil {
		return nil, fmt.Errorf("webrtc: new local track: %w", err)
	}
	return t, nil
}

// Pipe 把远端轨道的 RTP 包原样转发到本地轨道,直到远端轨道结束(对端停发 / 连接关闭)
// 或读到 EOF。纯包转发——不解码、不转码、不做抖动缓冲,是 SFU 转发的最小原语。
// local.WriteRTP 会扇出给所有 AddTrack 了该 local 轨道的 PeerConnection。
//
// 注意:ReadRTP 会阻塞,ctx 取消不会立即中断它;Pipe 主要靠远端轨道结束来返回。要强制
// 停止,关闭承载 remote 的 PeerConnection 即可解除阻塞。
func Pipe(ctx context.Context, remote *pion.TrackRemote, local *pion.TrackLocalStaticRTP) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		pkt, _, err := remote.ReadRTP()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("webrtc: read rtp: %w", err)
		}
		if err := local.WriteRTP(pkt); err != nil {
			// 所有订阅者都断开时 WriteRTP 返回 ErrClosedPipe,视为正常收尾。
			if errors.Is(err, io.ErrClosedPipe) {
				return nil
			}
			return fmt.Errorf("webrtc: write rtp: %w", err)
		}
	}
}
