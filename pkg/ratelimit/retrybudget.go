package ratelimit

import "sync"

// RetryBudget 是"重试预算"(gRPC retry throttling 模型):用一个令牌余额约束重试**占比**,
// 防止在下游普遍失败时,重试把下游进一步压垮(重试风暴)。它不限制单次重试的时机,而是限制
// "重试/请求"的整体比率——下游健康时余额充足、放行重试;下游持续失败会迅速耗尽余额、抑制重试。
//
// 模型:余额上限 maxTokens,初始为满;每完成一个请求(成功或不可重试的失败)Deposit 加 tokenRatio;
// 每次重试尝试 Allow 花 1 个令牌,且仅当余额 > maxTokens/2 时才放行。并发安全。
//
// 用法:
//
//	resp, err := call()
//	for retryable(err) && budget.Allow() {
//	    resp, err = call()
//	}
//	budget.Deposit() // 本次请求最终完成(无论成败),归还额度
type RetryBudget struct {
	mu        sync.Mutex
	tokens    float64
	max       float64
	threshold float64
	ratio     float64
}

// NewRetryBudget 创建重试预算。maxTokens 为余额上限(如 100),tokenRatio 为每个完成请求归还的令牌数
// (如 0.1,表示大约每 10 个正常请求才攒够 1 次重试的额度)。参数非法时用保守默认(max=100,ratio=0.1)。
func NewRetryBudget(maxTokens int, tokenRatio float64) *RetryBudget {
	max := float64(maxTokens)
	if max <= 0 {
		max = 100
	}
	if tokenRatio <= 0 {
		tokenRatio = 0.1
	}
	return &RetryBudget{
		tokens:    max, // 初始给满,避免冷启动时正常重试被误伤
		max:       max,
		threshold: max / 2,
		ratio:     tokenRatio,
	}
}

// Allow 尝试为"一次重试"花 1 个令牌:仅当余额 > 阈值(maxTokens/2)时放行并扣减,返回是否允许重试。
func (b *RetryBudget) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.tokens <= b.threshold {
		return false
	}
	b.tokens--
	return true
}

// Deposit 在一个请求最终完成(成功或不可重试失败)时调用,归还 tokenRatio 个令牌(封顶 maxTokens)。
func (b *RetryBudget) Deposit() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tokens += b.ratio
	if b.tokens > b.max {
		b.tokens = b.max
	}
}

// Tokens 返回当前余额(用于观测/调试)。
func (b *RetryBudget) Tokens() float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.tokens
}
