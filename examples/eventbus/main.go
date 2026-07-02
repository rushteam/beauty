// eventbus 示例:进程内按主题分发的事件总线。
//
// 演示 pkg/eventbus:多模块订阅同一事件、topic 隔离、退订。
// 场景:玩家上线事件 → 通知模块 + 审计模块 + 成就模块各自订阅,发布方无需感知。
package main

import (
	"fmt"

	"github.com/rushteam/beauty/pkg/eventbus"
)

type UserEvent struct {
	UserID string
	Extra  string
}

func main() {
	bus := eventbus.New[UserEvent]()

	// 三个模块订阅 "user.login"。
	bus.Subscribe("user.login", func(topic string, e UserEvent) {
		fmt.Printf("  [通知] 欢迎回来, %s\n", e.UserID)
	})
	bus.Subscribe("user.login", func(topic string, e UserEvent) {
		fmt.Printf("  [审计] 记录登录: %s (%s)\n", e.UserID, e.Extra)
	})
	achievementUnsub := bus.Subscribe("user.login", func(topic string, e UserEvent) {
		fmt.Printf("  [成就] 检查每日首登奖励: %s\n", e.UserID)
	})

	// 另一个 topic 独立。
	bus.Subscribe("user.logout", func(topic string, e UserEvent) {
		fmt.Printf("  [在线状态] %s 已下线\n", e.UserID)
	})

	fmt.Println("发布 user.login:")
	n := bus.Publish("user.login", UserEvent{UserID: "alice", Extra: "ip=1.2.3.4"})
	fmt.Printf("→ 通知了 %d 个订阅者\n", n)

	fmt.Println("\n发布 user.logout(只有在线状态模块收到):")
	bus.Publish("user.logout", UserEvent{UserID: "alice"})

	// 成就模块退订后不再收到。
	achievementUnsub()
	fmt.Println("\n成就模块退订后再发 user.login:")
	bus.Publish("user.login", UserEvent{UserID: "bob", Extra: "ip=5.6.7.8"})
}
