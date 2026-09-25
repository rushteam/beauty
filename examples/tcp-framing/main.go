// 原生 TCP 长连接网关示例:组合三个纯机制——
//   - tcpserver:接入、准入、优雅关停;
//   - framing:长度前缀分帧,解决粘包/半包(这里用 2 字节头);
//   - scandefender:扫描器/闪断防御,通过 OnAccept/OnClose 钩子接入(机制不做业务)。
//
// 另外用 WithReadTimeout 给空闲连接加超时,避免连接泄漏。
//
// 运行:
//
//	go run ./examples/tcp-framing
//
// 然后用任意客户端连上 127.0.0.1:9000,按 [2 字节大端长度][payload] 发送即可收到回显。
package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/service/tcpserver"
	"github.com/rushteam/beauty/pkg/transport/framing"
	"github.com/rushteam/beauty/pkg/transport/scandefender"
)

func main() {
	// 分帧编解码器:2 字节长度头,单帧最大 64 KiB。
	codec := framing.New(framing.WithHeaderSize(2), framing.WithMaxFrameSize(64<<10))

	// 扫描器防御:高频连接 / 闪断自动封禁。
	sd := scandefender.New()
	var connTimes sync.Map // remoteAddr -> time.Time,供 OnClose 计算连接时长

	handler := func(ctx context.Context, conn net.Conn) {
		r := codec.NewReader(conn)
		w := codec.NewWriter(conn)
		for {
			msg, err := r.ReadFrame()
			if err != nil {
				if !errors.Is(err, io.EOF) {
					slog.Debug("read frame end", "remote", conn.RemoteAddr().String(), "err", err)
				}
				return // EOF / 空闲超时 / 错误:退出即回收连接
			}
			// 业务在此处理 msg;这里只做回显。
			if err := w.WriteFrame(append([]byte("echo:"), msg...)); err != nil {
				return
			}
		}
	}

	srv := tcpserver.New(":9000", handler,
		tcpserver.WithReadTimeout(60*time.Second), // 60s 无数据即回收
		tcpserver.WithOnAccept(func(_ context.Context, conn net.Conn) error {
			addr := conn.RemoteAddr().String()
			if err := sd.Admit(addr); err != nil {
				return err // 被判定为扫描器,拒绝接入
			}
			connTimes.Store(addr, time.Now())
			return nil
		}),
		tcpserver.WithOnClose(func(conn net.Conn) {
			addr := conn.RemoteAddr().String()
			if v, ok := connTimes.LoadAndDelete(addr); ok {
				sd.OnClose(addr, v.(time.Time))
			}
		}),
	)

	app := beauty.New(beauty.WithService(srv))
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}
