package ratelimit

import "testing"

func TestRetryBudget_AllowsWhenHealthy(t *testing.T) {
	b := NewRetryBudget(100, 0.1) // 初始满(100 > 阈值 50)
	if !b.Allow() {
		t.Fatal("余额充足时应允许重试")
	}
}

func TestRetryBudget_ThrottlesWhenDrained(t *testing.T) {
	b := NewRetryBudget(100, 0.1)
	// 连续重试耗到阈值(50)以下:100 → 需要 >50 次 Allow 才会被拒。
	allowed := 0
	for i := 0; i < 200; i++ {
		if b.Allow() {
			allowed++
		}
	}
	// 只能从 100 花到 50,故约 50 次放行,之后一律拒绝。
	if allowed > 51 || allowed < 49 {
		t.Fatalf("耗尽后应在阈值处停住(约 50 次放行), got %d", allowed)
	}
	if b.Allow() {
		t.Fatal("低于阈值应拒绝重试")
	}
}

func TestRetryBudget_DepositRefills(t *testing.T) {
	b := NewRetryBudget(100, 0.5)
	for i := 0; i < 60; i++ { // 花到阈值以下
		b.Allow()
	}
	if b.Allow() {
		t.Fatal("此时应已被抑制")
	}
	// 大量正常请求归还额度,余额回升到阈值之上后应重新放行。
	for i := 0; i < 100; i++ {
		b.Deposit()
	}
	if !b.Allow() {
		t.Fatalf("归还后余额回升应重新允许重试, tokens=%.1f", b.Tokens())
	}
}

func TestRetryBudget_DepositCap(t *testing.T) {
	b := NewRetryBudget(10, 1)
	for i := 0; i < 100; i++ {
		b.Deposit()
	}
	if b.Tokens() > 10 {
		t.Fatalf("余额不应超过上限, got %.1f", b.Tokens())
	}
}
