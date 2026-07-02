// fsm 示例:有限状态机(对局生命周期)。
//
// 演示 pkg/fsm:声明式转移表、合法/非法转移校验、OnEnter/OnLeave 钩子。
// 典型场景:对局(等待→进行→结算)、房间生命周期、订单状态流转。
package main

import (
	"fmt"

	"github.com/rushteam/beauty/pkg/fsm"
)

type State string

const (
	Waiting State = "waiting"
	Playing State = "playing"
	Settled State = "settled"
)

type Event string

const (
	Start  Event = "start"
	Finish Event = "finish"
	Reset  Event = "reset"
)

func main() {
	m := fsm.NewBuilder[State, Event](Waiting).
		Allow(Waiting, Start, Playing).
		Allow(Playing, Finish, Settled).
		Allow(Settled, Reset, Waiting).
		OnEnter(func(to State, e Event) error {
			fmt.Printf("  → 进入状态 %q(事件 %q)\n", to, e)
			return nil
		}).
		Build()

	fmt.Printf("初始状态: %q\n", m.Current())

	// 合法流转:等待 → 进行 → 结算。
	fmt.Println("Fire(start):")
	m.Fire(Start)
	fmt.Println("Fire(finish):")
	m.Fire(Finish)

	// 非法流转:结算态不能再 finish。
	fmt.Println("Fire(finish) 在 settled 态(非法):")
	if _, err := m.Fire(Finish); err != nil {
		fmt.Printf("  ✗ 被拒: %v\n", err)
	}

	// 查询能力。
	fmt.Printf("当前可触发事件: %v\n", m.AvailableEvents())
	fmt.Printf("能否 reset: %v\n", m.Can(Reset))

	fmt.Println("Fire(reset):")
	m.Fire(Reset)
	fmt.Printf("最终状态: %q\n", m.Current())
}
