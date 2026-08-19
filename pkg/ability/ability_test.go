package ability_test

import (
	"errors"
	"testing"

	"github.com/rushteam/beauty/pkg/ability"
)

// ---------- 测试用业务类型 ----------

type entity struct {
	ID string
	HP int
	MP int
}

type resType string

const mpResource resType = "mp"

// 资源检查器:只看 MP。
var resChecker = ability.ResourceFunc[*entity, resType]{
	CheckFn: func(caster *entity, costs []ability.Cost[resType]) bool {
		for _, c := range costs {
			if c.Resource == mpResource && float64(caster.MP) < c.Amount {
				return false
			}
		}
		return true
	},
	SpendFn: func(caster *entity, costs []ability.Cost[resType]) {
		for _, c := range costs {
			if c.Resource == mpResource {
				caster.MP -= int(c.Amount)
			}
		}
	},
}

// 即时伤害阶段:一帧完成。
func instantDamage(source, target *entity, dmg float64) ability.Phase[*entity] {
	return ability.PhaseFunc[*entity](func(frame uint64) ability.PhaseResult[*entity] {
		return ability.PhaseResult[*entity]{
			Status: ability.PhaseComplete,
			Effects: []ability.Effect[*entity]{
				{Tag: "damage", Value: dmg, Source: source, Target: target},
			},
			Cues: []ability.Cue{
				{Tag: "vfx", Asset: "fireball_hit"},
			},
		}
	})
}

// 蓄力阶段:持续 N 帧。
type chargePhase struct {
	duration int
	elapsed  int
}

func newCharge(duration int) *chargePhase {
	return &chargePhase{duration: duration}
}

func (c *chargePhase) Tick(frame uint64) ability.PhaseResult[*entity] {
	c.elapsed++
	if c.elapsed >= c.duration {
		return ability.PhaseResult[*entity]{
			Status: ability.PhaseComplete,
			Cues:   []ability.Cue{{Tag: "vfx", Asset: "charge_complete"}},
		}
	}
	return ability.PhaseResult[*entity]{
		Status: ability.PhaseRunning,
		Cues:   []ability.Cue{{Tag: "vfx", Asset: "charging"}},
	}
}

func (c *chargePhase) Reset() { c.elapsed = 0 }

func TestCast_InstantSkill(t *testing.T) {
	caster := &entity{ID: "hero", HP: 100, MP: 50}
	target := &entity{ID: "mob", HP: 100}
	runner := ability.NewRunner[*entity, resType](caster, resChecker)

	fireball := &ability.Spec[*entity, resType]{
		ID:    "fireball",
		Costs: []ability.Cost[resType]{{Resource: mpResource, Amount: 20}},
		Phases: []ability.Phase[*entity]{
			instantDamage(caster, target, 30),
		},
	}

	if err := runner.Cast(fireball, 1); err != nil {
		t.Fatalf("Cast: %v", err)
	}
	if caster.MP != 30 {
		t.Fatalf("MP = %d, want 30", caster.MP)
	}

	r := runner.Tick(1)
	if !r.Active {
		t.Fatal("should be active")
	}
	if !r.Done {
		t.Fatal("instant skill should complete in one tick")
	}
	if len(r.Effects) != 1 || r.Effects[0].Tag != "damage" || r.Effects[0].Value != 30 {
		t.Fatalf("effects = %+v", r.Effects)
	}
	if len(r.Cues) != 1 || r.Cues[0].Asset != "fireball_hit" {
		t.Fatalf("cues = %+v", r.Cues)
	}
	if !runner.IsIdle() {
		t.Fatal("should be idle after completion")
	}
}

func TestCast_MultiPhaseSkill(t *testing.T) {
	caster := &entity{ID: "mage", HP: 100, MP: 100}
	target := &entity{ID: "boss", HP: 500}
	runner := ability.NewRunner[*entity, resType](caster, resChecker)

	// 蓄力 3 帧 → 释放伤害
	ultimate := &ability.Spec[*entity, resType]{
		ID:    "ultimate",
		Costs: []ability.Cost[resType]{{Resource: mpResource, Amount: 50}},
		Phases: []ability.Phase[*entity]{
			newCharge(3),
			instantDamage(caster, target, 100),
		},
	}

	if err := runner.Cast(ultimate, 1); err != nil {
		t.Fatalf("Cast: %v", err)
	}

	// 蓄力阶段:3 帧 Running
	for frame := uint64(1); frame <= 2; frame++ {
		r := runner.Tick(frame)
		if !r.Active || r.Done || r.Cancel {
			t.Fatalf("frame %d: should still be charging", frame)
		}
		if runner.CurrentSkill() != "ultimate" {
			t.Fatalf("current skill = %s", runner.CurrentSkill())
		}
	}
	// 第 3 帧:蓄力完成(PhaseComplete),切到下一阶段
	r := runner.Tick(3)
	if !r.Active || r.Done {
		t.Fatalf("frame 3: should be active, phase 1 done → phase 2")
	}

	// 第 4 帧:释放阶段(即时完成)
	r = runner.Tick(4)
	if !r.Done {
		t.Fatal("frame 4: should complete")
	}
	if len(r.Effects) != 1 || r.Effects[0].Value != 100 {
		t.Fatalf("effects = %+v", r.Effects)
	}
	if !runner.IsIdle() {
		t.Fatal("should be idle")
	}
}

func TestCast_Cooldown(t *testing.T) {
	caster := &entity{ID: "hero", MP: 999}
	target := &entity{ID: "mob", HP: 100}
	runner := ability.NewRunner[*entity, resType](caster, resChecker)

	skill := &ability.Spec[*entity, resType]{
		ID:           "slash",
		CooldownTick: 5,
		Phases:       []ability.Phase[*entity]{instantDamage(caster, target, 10)},
	}

	// 施放成功
	if err := runner.Cast(skill, 10); err != nil {
		t.Fatalf("Cast: %v", err)
	}
	runner.Tick(10) // 完成

	// 冷却中
	cd := runner.CooldownRemaining("slash", 12)
	if cd != 3 {
		t.Fatalf("cooldown remaining = %d, want 3", cd)
	}
	err := runner.Cast(skill, 12)
	if !errors.Is(err, ability.ErrOnCooldown) {
		t.Fatalf("should be on cooldown, got %v", err)
	}

	// 冷却结束
	if err := runner.Cast(skill, 15); err != nil {
		t.Fatalf("should be ready at frame 15: %v", err)
	}
}

func TestCast_InsufficientResource(t *testing.T) {
	caster := &entity{ID: "hero", MP: 5}
	runner := ability.NewRunner[*entity, resType](caster, resChecker)

	expensive := &ability.Spec[*entity, resType]{
		ID:    "meteor",
		Costs: []ability.Cost[resType]{{Resource: mpResource, Amount: 100}},
		Phases: []ability.Phase[*entity]{
			ability.PhaseFunc[*entity](func(frame uint64) ability.PhaseResult[*entity] {
				return ability.PhaseResult[*entity]{Status: ability.PhaseComplete}
			}),
		},
	}

	err := runner.Cast(expensive, 1)
	if !errors.Is(err, ability.ErrNoResource) {
		t.Fatalf("want ErrNoResource, got %v", err)
	}
	if caster.MP != 5 {
		t.Fatal("MP should not be deducted")
	}
}

func TestCast_WhileBusy(t *testing.T) {
	caster := &entity{ID: "hero", MP: 100}
	runner := ability.NewRunner[*entity, resType](caster, resChecker)

	slow := &ability.Spec[*entity, resType]{
		ID:     "channel",
		Phases: []ability.Phase[*entity]{newCharge(10)},
	}
	fast := &ability.Spec[*entity, resType]{
		ID: "quick",
		Phases: []ability.Phase[*entity]{
			ability.PhaseFunc[*entity](func(frame uint64) ability.PhaseResult[*entity] {
				return ability.PhaseResult[*entity]{Status: ability.PhaseComplete}
			}),
		},
	}

	runner.Cast(slow, 1)
	runner.Tick(1) // Running

	err := runner.Cast(fast, 2)
	if !errors.Is(err, ability.ErrBusy) {
		t.Fatalf("want ErrBusy, got %v", err)
	}
}

func TestCancel_ExternalInterrupt(t *testing.T) {
	caster := &entity{ID: "hero", MP: 100}
	runner := ability.NewRunner[*entity, resType](caster, resChecker)

	skill := &ability.Spec[*entity, resType]{
		ID:     "meditate",
		Phases: []ability.Phase[*entity]{newCharge(100)},
	}

	runner.Cast(skill, 1)
	runner.Tick(1)
	if runner.IsIdle() {
		t.Fatal("should be casting")
	}

	runner.Cancel()
	if !runner.IsIdle() {
		t.Fatal("should be idle after cancel")
	}

	// Cancel 后可以立即施放新技能
	if err := runner.Cast(skill, 2); err != nil {
		t.Fatalf("should be castable after cancel: %v", err)
	}
}

func TestPhase_SelfCancel(t *testing.T) {
	caster := &entity{ID: "hero", MP: 100}
	runner := ability.NewRunner[*entity, resType](caster, resChecker)

	cancelPhase := ability.PhaseFunc[*entity](func(frame uint64) ability.PhaseResult[*entity] {
		return ability.PhaseResult[*entity]{Status: ability.PhaseCancelled}
	})
	skill := &ability.Spec[*entity, resType]{
		ID:     "broken",
		Phases: []ability.Phase[*entity]{cancelPhase},
	}

	runner.Cast(skill, 1)
	r := runner.Tick(1)
	if !r.Cancel {
		t.Fatal("should be cancelled")
	}
	if !runner.IsIdle() {
		t.Fatal("should be idle")
	}
}

func TestCast_NoPhases(t *testing.T) {
	caster := &entity{ID: "hero", MP: 100}
	runner := ability.NewRunner[*entity, resType](caster, resChecker)

	empty := &ability.Spec[*entity, resType]{ID: "empty"}
	err := runner.Cast(empty, 1)
	if !errors.Is(err, ability.ErrNoPhases) {
		t.Fatalf("want ErrNoPhases, got %v", err)
	}
}

func TestCast_ReusableAfterCompletion(t *testing.T) {
	caster := &entity{ID: "hero", MP: 999}
	target := &entity{ID: "mob", HP: 100}
	runner := ability.NewRunner[*entity, resType](caster, resChecker)

	skill := &ability.Spec[*entity, resType]{
		ID:     "basic_attack",
		Phases: []ability.Phase[*entity]{instantDamage(caster, target, 5)},
	}

	for i := 0; i < 3; i++ {
		if err := runner.Cast(skill, uint64(i*10)); err != nil {
			t.Fatalf("cast %d: %v", i, err)
		}
		r := runner.Tick(uint64(i * 10))
		if !r.Done {
			t.Fatalf("cast %d: should complete", i)
		}
	}
}

func TestTick_WhenIdle_ReturnsEmpty(t *testing.T) {
	caster := &entity{ID: "hero", MP: 100}
	runner := ability.NewRunner[*entity, resType](caster, resChecker)

	r := runner.Tick(1)
	if r.Active || r.Done || r.Cancel || len(r.Effects) > 0 {
		t.Fatalf("idle tick should be empty: %+v", r)
	}
}
