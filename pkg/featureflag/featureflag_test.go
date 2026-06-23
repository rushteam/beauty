package featureflag

import (
	"fmt"
	"math"
	"testing"
)

func TestIsEnabled_MasterSwitch(t *testing.T) {
	e := New()
	e.SetFlag(Flag{Key: "f", Enabled: false})
	if e.IsEnabled("f", "u1", nil) {
		t.Fatal("disabled flag must be off")
	}
	e.SetFlag(Flag{Key: "f", Enabled: true}) // Rollout 0 → 默认 1
	if !e.IsEnabled("f", "u1", nil) {
		t.Fatal("enabled flag with full rollout must be on")
	}
}

func TestIsEnabled_UnknownFlag(t *testing.T) {
	if New().IsEnabled("nope", "u", nil) {
		t.Fatal("unknown flag must be off")
	}
}

func TestIsEnabled_Deterministic(t *testing.T) {
	e := New()
	e.SetFlag(Flag{Key: "f", Enabled: true, Rollout: 0.5})
	first := e.IsEnabled("f", "user-42", nil)
	for range 100 {
		if e.IsEnabled("f", "user-42", nil) != first {
			t.Fatal("same id must yield stable result")
		}
	}
}

func TestIsEnabled_RolloutDistribution(t *testing.T) {
	e := New()
	e.SetFlag(Flag{Key: "f", Enabled: true, Rollout: 0.3})
	on := 0
	const total = 10000
	for i := range total {
		if e.IsEnabled("f", fmt.Sprintf("user-%d", i), nil) {
			on++
		}
	}
	ratio := float64(on) / total
	if math.Abs(ratio-0.3) > 0.03 {
		t.Fatalf("rollout ~0.3 expected, got %.3f", ratio)
	}
}

func TestIsEnabled_TargetingRuleForces(t *testing.T) {
	e := New()
	e.SetFlag(Flag{
		Key:     "f",
		Enabled: false, // 总开关关
		Rules: []Rule{
			{When: map[string]any{"plan": "pro"}, Force: true}, // pro 用户强制开
		},
	})
	if !e.IsEnabled("f", "u", Attributes{"plan": "pro"}) {
		t.Fatal("pro user should be forced on by rule")
	}
	if e.IsEnabled("f", "u", Attributes{"plan": "free"}) {
		t.Fatal("free user should stay off")
	}
}

func TestVariant_AssignmentAndStability(t *testing.T) {
	e := New()
	e.SetExperiment(Experiment{Key: "exp", Variants: []string{"a", "b"}})

	v, ok := e.Variant("exp", "user-7")
	if !ok || (v != "a" && v != "b") {
		t.Fatalf("expected a/b, got %q ok=%v", v, ok)
	}
	for range 50 {
		v2, _ := e.Variant("exp", "user-7")
		if v2 != v {
			t.Fatal("variant must be stable for same id")
		}
	}
}

func TestVariant_Distribution(t *testing.T) {
	e := New()
	e.SetExperiment(Experiment{Key: "exp", Variants: []string{"a", "b"}}) // 等分
	counts := map[string]int{}
	const total = 10000
	for i := range total {
		v, ok := e.Variant("exp", fmt.Sprintf("u-%d", i))
		if ok {
			counts[v]++
		}
	}
	for _, name := range []string{"a", "b"} {
		ratio := float64(counts[name]) / total
		if math.Abs(ratio-0.5) > 0.03 {
			t.Fatalf("variant %s ~0.5 expected, got %.3f", name, ratio)
		}
	}
}

func TestVariant_Coverage(t *testing.T) {
	e := New()
	e.SetExperiment(Experiment{Key: "exp", Variants: []string{"a"}, Coverage: 0.2})
	in := 0
	const total = 10000
	for i := range total {
		if _, ok := e.Variant("exp", fmt.Sprintf("u-%d", i)); ok {
			in++
		}
	}
	ratio := float64(in) / total
	if math.Abs(ratio-0.2) > 0.03 {
		t.Fatalf("coverage ~0.2 expected, got %.3f", ratio)
	}
}

func TestVariant_WeightedSplit(t *testing.T) {
	e := New()
	e.SetExperiment(Experiment{
		Key:      "exp",
		Variants: []string{"a", "b"},
		Weights:  []float64{0.8, 0.2},
	})
	counts := map[string]int{}
	const total = 10000
	for i := range total {
		v, ok := e.Variant("exp", fmt.Sprintf("u-%d", i))
		if ok {
			counts[v]++
		}
	}
	ra := float64(counts["a"]) / total
	if math.Abs(ra-0.8) > 0.03 {
		t.Fatalf("variant a ~0.8 expected, got %.3f", ra)
	}
}
