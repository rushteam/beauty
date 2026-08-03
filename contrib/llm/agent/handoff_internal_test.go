package agent

import (
	"strings"
	"testing"
)

// handoffTracker 白盒单测:超限 与 重复打转 两种护栏。
func TestHandoffTracker(t *testing.T) {
	// MaxHandoffs=2:第 3 次 record 触发。
	tr := &handoffTracker{cfg: HandoffConfig{MaxHandoffs: 2}}
	if err := tr.record("x"); err != nil {
		t.Fatal(err)
	}
	if err := tr.record("y"); err != nil {
		t.Fatal(err)
	}
	if err := tr.record("z"); err == nil || !strings.Contains(err.Error(), "max handoffs") {
		t.Fatalf("want max handoffs, got %v", err)
	}

	// Window=3, MinUnique=2:连续同一目标 3 次触发重复检测。
	tr2 := &handoffTracker{cfg: HandoffConfig{Window: 3, MinUnique: 2}}
	_ = tr2.record("a")
	_ = tr2.record("a")
	if err := tr2.record("a"); err == nil || !strings.Contains(err.Error(), "repetitive") {
		t.Fatalf("want repetitive guard, got %v", err)
	}
}

// parseHandoff 解析各种形态。
func TestParseHandoff(t *testing.T) {
	cases := []struct {
		in         string
		wantTarget string
		wantInput  string
		wantOK     bool
	}{
		{"HANDOFF: writer 写报告", "writer", "写报告", true},
		{"前言\nHANDOFF: b\n尾巴", "b", "", true},
		{"没有移交", "", "", false},
		{"HANDOFF:", "", "", false},
		{"  HANDOFF:   writer   多  个  空格  ", "writer", "多  个  空格", true},
	}
	for _, c := range cases {
		gt, gi, ok := parseHandoff(c.in)
		if gt != c.wantTarget || gi != c.wantInput || ok != c.wantOK {
			t.Errorf("parseHandoff(%q) = (%q,%q,%v), want (%q,%q,%v)", c.in, gt, gi, ok, c.wantTarget, c.wantInput, c.wantOK)
		}
	}
}
