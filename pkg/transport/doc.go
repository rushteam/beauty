// Package transport 提供实时传输与长连接层,解决"客户端与服务端之间的持久通道"问题。
//
// 选包标准:
//   - 涉及持久连接 / 推送 / 双向通信(WebSocket、SSE、QUIC、P2P 等)
//   - 如果只是一次性 HTTP 请求/响应,不属于此组(属于 api/ 或 service/webserver)
//   - 可依赖 foundation/、messaging/(如 stream)、api/(如 token)
//
// 已有子包:
//
//	ws        — WebSocket 薄封装(基于 coder/websocket)
//	sse       — Server-Sent Events 封装
//	quic      — QUIC 连接层(基于 quic-go)
//	p2p       — P2P 双通道抽象(Transport/PeerConn/Network/Topology/Signaling)
//	presence  — 在线状态追踪(含 status 子包)
//	router    — 多语义消息路由(pub/sub + request/reply + push)
//	resume    — 断线重连与消息补发
package transport
