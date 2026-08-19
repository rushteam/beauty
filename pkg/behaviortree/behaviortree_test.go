package behaviortree_test

import (
	"testing"

	bt "github.com/rushteam/beauty/pkg/behaviortree"
)

// 黑板:MOBA 小兵 AI 上下文。
type minionBB struct {
	EnemyInRange bool
	HP           int
	MaxHP        int
	AttackCount  int
	PatrolCount  int
	RetreatCount int
	HealCount    int
	RunningTicks int
}

func attack[T *minionBB]() bt.Action[minionBB] {
	return func(bb *minionBB) bt.Status {
		bb.AttackCount++
		return bt.Success
	}
}

func patrol() bt.Action[minionBB] {
	return func(bb *minionBB) bt.Status {
		bb.PatrolCount++
		return bt.Success
	}
}

func retreat() bt.Action[minionBB] {
	return func(bb *minionBB) bt.Status {
		bb.RetreatCount++
		return bt.Success
	}
}

func heal() bt.Action[minionBB] {
	return func(bb *minionBB) bt.Status {
		bb.HealCount++
		return bt.Success
	}
}

func isLowHP() bt.Condition[minionBB] {
	return func(bb *minionBB) bool {
		return bb.HP < bb.MaxHP/4
	}
}

func hasEnemy() bt.Condition[minionBB] {
	return func(bb *minionBB) bool {
		return bb.EnemyInRange
	}
}

// TestSequence_AllSuccess 全部子节点成功 → Success。
func TestSequence_AllSuccess(t *testing.T) {
	seq := bt.Sequence[minionBB](hasEnemy(), attack[*minionBB]())
	bb := &minionBB{EnemyInRange: true, HP: 100, MaxHP: 100}
	if s := seq.Tick(bb); s != bt.Success {
		t.Fatalf("want Success, got %v", s)
	}
	if bb.AttackCount != 1 {
		t.Fatalf("attack not called")
	}
}

// TestSequence_EarlyFailure 条件不满足 → Failure,后续不执行。
func TestSequence_EarlyFailure(t *testing.T) {
	seq := bt.Sequence[minionBB](hasEnemy(), attack[*minionBB]())
	bb := &minionBB{EnemyInRange: false}
	if s := seq.Tick(bb); s != bt.Failure {
		t.Fatalf("want Failure, got %v", s)
	}
	if bb.AttackCount != 0 {
		t.Fatal("attack should not be called")
	}
}

// TestSelector_FirstSuccess 第一个成功即返回 Success。
func TestSelector_FirstSuccess(t *testing.T) {
	sel := bt.Selector[minionBB](
		bt.Sequence[minionBB](isLowHP(), retreat()),
		bt.Sequence[minionBB](hasEnemy(), attack[*minionBB]()),
		patrol(),
	)
	bb := &minionBB{EnemyInRange: true, HP: 100, MaxHP: 100}
	if s := sel.Tick(bb); s != bt.Success {
		t.Fatalf("want Success, got %v", s)
	}
	if bb.AttackCount != 1 {
		t.Fatal("should attack")
	}
	if bb.RetreatCount != 0 {
		t.Fatal("should not retreat")
	}
}

// TestSelector_Fallback 前面失败,最终 fallback 到巡逻。
func TestSelector_Fallback(t *testing.T) {
	sel := bt.Selector[minionBB](
		bt.Sequence[minionBB](isLowHP(), retreat()),
		bt.Sequence[minionBB](hasEnemy(), attack[*minionBB]()),
		patrol(),
	)
	bb := &minionBB{EnemyInRange: false, HP: 100, MaxHP: 100}
	if s := sel.Tick(bb); s != bt.Success {
		t.Fatalf("want Success, got %v", s)
	}
	if bb.PatrolCount != 1 {
		t.Fatal("should patrol")
	}
}

// TestInverter 取反。
func TestInverter(t *testing.T) {
	inv := bt.Inverter[minionBB](hasEnemy())
	bb := &minionBB{EnemyInRange: true}
	if s := inv.Tick(bb); s != bt.Failure {
		t.Fatalf("inverted true should be Failure, got %v", s)
	}
	bb.EnemyInRange = false
	if s := inv.Tick(bb); s != bt.Success {
		t.Fatalf("inverted false should be Success, got %v", s)
	}
}

// TestSucceeder 强制成功。
func TestSucceeder(t *testing.T) {
	always := bt.Succeeder[minionBB](bt.Action[minionBB](func(bb *minionBB) bt.Status {
		return bt.Failure
	}))
	bb := &minionBB{}
	if s := always.Tick(bb); s != bt.Success {
		t.Fatalf("Succeeder should return Success, got %v", s)
	}
}

// TestGuard 守卫节点。
func TestGuard(t *testing.T) {
	g := bt.Guard[minionBB](func(bb *minionBB) bool {
		return bb.EnemyInRange
	}, attack[*minionBB]())
	bb := &minionBB{EnemyInRange: false}
	if s := g.Tick(bb); s != bt.Failure {
		t.Fatalf("Guard should fail without enemy, got %v", s)
	}
	bb.EnemyInRange = true
	if s := g.Tick(bb); s != bt.Success {
		t.Fatalf("Guard should succeed with enemy, got %v", s)
	}
}

// TestRunning_Propagation Running 透传。
func TestRunning_Propagation(t *testing.T) {
	longAction := bt.Action[minionBB](func(bb *minionBB) bt.Status {
		bb.RunningTicks++
		if bb.RunningTicks < 3 {
			return bt.Running
		}
		return bt.Success
	})
	seq := bt.Sequence[minionBB](hasEnemy(), longAction)
	bb := &minionBB{EnemyInRange: true}
	for range 2 {
		if s := seq.Tick(bb); s != bt.Running {
			t.Fatalf("should be Running, got %v", s)
		}
	}
	if s := seq.Tick(bb); s != bt.Success {
		t.Fatalf("should complete, got %v", s)
	}
}

// TestMemSequence 记忆型顺序节点:从上次 Running 处继续。
func TestMemSequence(t *testing.T) {
	callCount := 0
	step1 := bt.Action[minionBB](func(bb *minionBB) bt.Status {
		callCount++
		return bt.Success
	})
	step2 := bt.Action[minionBB](func(bb *minionBB) bt.Status {
		bb.RunningTicks++
		if bb.RunningTicks < 2 {
			return bt.Running
		}
		return bt.Success
	})
	mem := bt.MemSequence[minionBB](step1, step2)
	bb := &minionBB{}

	// 第一次 Tick:step1 成功,step2 Running
	if s := mem.Tick(bb); s != bt.Running {
		t.Fatalf("tick 1: want Running, got %v", s)
	}
	if callCount != 1 {
		t.Fatalf("step1 should be called once, got %d", callCount)
	}
	// 第二次 Tick:从 step2 继续(step1 被跳过)
	if s := mem.Tick(bb); s != bt.Success {
		t.Fatalf("tick 2: want Success, got %v", s)
	}
	if callCount != 1 {
		t.Fatalf("step1 should NOT be re-called, got %d", callCount)
	}
}

// TestParallel_RequireAll 全部成功才成功。
func TestParallel_RequireAll(t *testing.T) {
	p := bt.Parallel[minionBB](bt.RequireAll,
		attack[*minionBB](),
		patrol(),
	)
	bb := &minionBB{}
	if s := p.Tick(bb); s != bt.Success {
		t.Fatalf("all success → Success, got %v", s)
	}
	if bb.AttackCount != 1 || bb.PatrolCount != 1 {
		t.Fatal("both should be called")
	}
}

// TestParallel_RequireAll_AnyFail 任一失败则失败。
func TestParallel_RequireAll_AnyFail(t *testing.T) {
	fail := bt.Action[minionBB](func(bb *minionBB) bt.Status { return bt.Failure })
	p := bt.Parallel[minionBB](bt.RequireAll, attack[*minionBB](), fail)
	bb := &minionBB{}
	if s := p.Tick(bb); s != bt.Failure {
		t.Fatalf("one fail → Failure, got %v", s)
	}
}

// TestParallel_RequireOne 任一成功即成功。
func TestParallel_RequireOne(t *testing.T) {
	fail := bt.Action[minionBB](func(bb *minionBB) bt.Status { return bt.Failure })
	p := bt.Parallel[minionBB](bt.RequireOne, fail, attack[*minionBB]())
	bb := &minionBB{}
	if s := p.Tick(bb); s != bt.Success {
		t.Fatalf("one success → Success, got %v", s)
	}
}

// TestRepeater 重复 N 次。
func TestRepeater(t *testing.T) {
	r := bt.Repeater[minionBB](3, attack[*minionBB]())
	bb := &minionBB{}
	if s := r.Tick(bb); s != bt.Success {
		t.Fatalf("3 repeats → Success, got %v", s)
	}
	if bb.AttackCount != 3 {
		t.Fatalf("should attack 3 times, got %d", bb.AttackCount)
	}
}

// TestRepeater_FailAborts 子节点 Failure 提前中止。
func TestRepeater_FailAborts(t *testing.T) {
	count := 0
	failOnSecond := bt.Action[minionBB](func(bb *minionBB) bt.Status {
		count++
		if count == 2 {
			return bt.Failure
		}
		return bt.Success
	})
	r := bt.Repeater[minionBB](5, failOnSecond)
	bb := &minionBB{}
	if s := r.Tick(bb); s != bt.Failure {
		t.Fatalf("failure should abort, got %v", s)
	}
	if count != 2 {
		t.Fatalf("should stop at 2, got %d", count)
	}
}

// TestRepeatUntilFail 重复直到失败。
func TestRepeatUntilFail(t *testing.T) {
	count := 0
	failAfter3 := bt.Action[minionBB](func(bb *minionBB) bt.Status {
		count++
		if count > 3 {
			return bt.Failure
		}
		return bt.Success
	})
	r := bt.RepeatUntilFail[minionBB](failAfter3)
	// Running 直到子节点 Failure,此时返回 Success。
	for range 3 {
		if s := r.Tick(bb()); s != bt.Running {
			t.Fatalf("should be Running, got %v", s)
		}
	}
	if s := r.Tick(bb()); s != bt.Success {
		t.Fatalf("should be Success after failure, got %v", s)
	}
}

func bb() *minionBB { return &minionBB{} }

// TestCooldown 冷却装饰器。
func TestCooldown(t *testing.T) {
	cd := bt.Cooldown[minionBB](2, attack[*minionBB]())
	bb := &minionBB{}
	if s := cd.Tick(bb); s != bt.Success {
		t.Fatalf("first tick should succeed, got %v", s)
	}
	// 冷却中:2 次 Tick 返回 Failure
	for range 2 {
		if s := cd.Tick(bb); s != bt.Failure {
			t.Fatalf("cooling down should Failure, got %v", s)
		}
	}
	// 冷却结束
	if s := cd.Tick(bb); s != bt.Success {
		t.Fatalf("after cooldown should succeed, got %v", s)
	}
}

// TestComplexTree 复合 AI:低血量撤退+治疗 > 有敌人攻击 > 巡逻。
func TestComplexTree(t *testing.T) {
	tree := bt.Selector[minionBB](
		bt.Sequence[minionBB](isLowHP(), retreat(), heal()),
		bt.Sequence[minionBB](hasEnemy(), attack[*minionBB]()),
		patrol(),
	)
	// 场景 1:低血量 → 撤退+治疗
	bb1 := &minionBB{HP: 10, MaxHP: 100, EnemyInRange: true}
	if s := tree.Tick(bb1); s != bt.Success {
		t.Fatalf("low hp: want Success, got %v", s)
	}
	if bb1.RetreatCount != 1 || bb1.HealCount != 1 {
		t.Fatal("should retreat and heal")
	}
	if bb1.AttackCount != 0 {
		t.Fatal("should not attack when retreating")
	}

	// 场景 2:正常血量 + 有敌人 → 攻击
	bb2 := &minionBB{HP: 80, MaxHP: 100, EnemyInRange: true}
	if s := tree.Tick(bb2); s != bt.Success {
		t.Fatalf("combat: want Success, got %v", s)
	}
	if bb2.AttackCount != 1 {
		t.Fatal("should attack")
	}

	// 场景 3:无敌人 → 巡逻
	bb3 := &minionBB{HP: 100, MaxHP: 100, EnemyInRange: false}
	if s := tree.Tick(bb3); s != bt.Success {
		t.Fatalf("patrol: want Success, got %v", s)
	}
	if bb3.PatrolCount != 1 {
		t.Fatal("should patrol")
	}
}

// TestStatus_String 状态的字符串表示。
func TestStatus_String(t *testing.T) {
	if bt.Running.String() != "Running" {
		t.Fatal("Running.String()")
	}
	if bt.Success.String() != "Success" {
		t.Fatal("Success.String()")
	}
	if bt.Failure.String() != "Failure" {
		t.Fatal("Failure.String()")
	}
}
