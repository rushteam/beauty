// p2p-signaling demo:浏览器 P2P DataChannel 通信。多个浏览器标签页通过信令服务器
// 自动发现彼此,建立 WebRTC DataChannel 直连——之后的数据传输不经服务器。
//
//	浏览器A ─┐                                   ┌─ 直连 DataChannel
//	浏览器B ─┼── WS 信令(offer/answer/ICE) ──→ ─┤  · 可靠通道:聊天消息
//	浏览器C ─┘       pkg/p2p/signaling            └─ · 不可靠通道:位置同步
//
// 运行:
//
//	go run ./examples/p2p-signaling
//	# 多个浏览器/标签页打开 http://localhost:8080/,输入相同房间名加入
//	# peer 间可发送聊天消息(可靠通道)和坐标更新(不可靠通道)
//
// 与 webrtc-voice-room 的区别:
//   - voice-room 是 SFU 模式:音视频经服务器转发(peer↔server)
//   - 本 demo 是 P2P mesh:数据在浏览器之间直连流动(peer↔peer),服务器只做配对
//
// 边界:鉴权、STUN/TURN、房间上限由上层控制。跨 NAT 需要 TURN。
package main

import (
	"context"
	"embed"
	"io/fs"
	"net/http"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/service/webserver"
	"github.com/rushteam/beauty/pkg/transport/p2p/signaling"
	"github.com/rushteam/beauty/pkg/transport/p2p/topology"
	"github.com/rushteam/beauty/pkg/transport/ws"
)

//go:embed static
var staticFiles embed.FS

func main() {
	// 信令服务器:使用 FullMesh 拓扑(所有 peer 互连)
	sig := signaling.NewServer(
		signaling.WithTopology(topology.FullMesh{}),
		signaling.WithWSOptions(ws.WithOriginPatterns("*")),
	)

	staticFS, _ := fs.Sub(staticFiles, "static")

	mux := http.NewServeMux()
	mux.Handle("/ws", sig.Handler())
	mux.Handle("/", http.FileServer(http.FS(staticFS)))

	app := beauty.New(
		beauty.WithWebServer(":8080", mux, webserver.WithServiceName("p2p-signaling")),
	)
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}
