// 远程 Web 控制台示例:注册自定义命令与推送主题,浏览器交互式调试/运维。
//
// 运行:
//
//	go run ./examples/console
//
// 然后浏览器打开 http://127.0.0.1:6070/console,试试:
//   - help            列出命令
//   - env / mem / gc  运行时信息
//   - goroutine       goroutine 数量
//   - deadlock        疑似死锁/泄漏检测
//   - echo hello      自定义命令
//   - sub clock       订阅时钟推送(每秒一次)
//
// 本示例开启了鉴权(admin/secret),受保护命令需先在页面右下角登录。
package main

import (
	"context"
	"strings"
	"time"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/service/console"
)

func main() {
	c := console.New(
		console.WithAddr("127.0.0.1:6070"),
		console.WithTitle("beauty console demo"),
		console.WithUser("admin", "secret"),
	)

	// 自定义命令:FlagPublic 表示无需登录即可执行。
	c.Register(&console.Command{
		Name:    "echo",
		Note:    "回显参数",
		Example: "echo hello world",
		Flag:    console.FlagPublic,
		Handler: func(_ context.Context, cc *console.Ctx) (string, error) {
			return strings.Join(cc.Args[1:], " "), nil
		},
	})

	// 自定义推送主题:订阅后每秒推送一次当前时间。
	c.RegisterTopic(&console.Topic{
		Name:     "clock",
		Note:     "当前服务器时间,每秒推送",
		Interval: time.Second,
		Build:    func() string { return time.Now().Format("2006-01-02 15:04:05") },
	})

	app := beauty.New(beauty.WithService(c))
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}
