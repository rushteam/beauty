// Package tournament 提供锦标赛(时间窗排行榜):在 pkg/leaderboard 之上
// 增加"按 cron 表达式周期性重置"的能力,每周期一个独立榜单,到期自动滚动到下一周期。
//
// 设计参考 Nakama server/core_tournament.go:
//   - Tournament = Leaderboard + 时间窗(start/end/resetSchedule);
//   - 每个周期用 expiryTime(下一次重置的 unix 秒)作为 leaderboard.RankCache 的
//     expiry 维度,天然实现"每周期独立榜单",无需显式清榜;
//   - calculateTournamentDeadlines 处理"启动延迟、中途插入、周期不对齐"等边界。
//
// 与 pkg/leaderboard 的关系:本包不重新实现排名,而是薄层封装 RankCache,
// 把"当前应该用哪个 expiry"算出来后委托给 RankCache.Fill/Insert/Get/TopN。
//
// 适用场景:赛季排行榜、每日/每周挑战、限时活动榜、任何"周期性重置的排名"。
//
// 零值不可用,用 New 构造。Tournament 并发安全。
package tournament

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/rushteam/beauty/pkg/leaderboard"
)

// Tournament 管理一个周期性重置的排行榜。
type Tournament struct {
	mu          sync.RWMutex
	id          string
	order       leaderboard.SortOrder
	schedule    cron.Schedule // cron 解析后的调度,Next(t) 算下一次重置
	durationSec int64         // 每周期时长(秒);0 表示不结束(单次榜)
	startDelay  int64         // 启动延迟(秒):首个周期开始时间偏移
	rc          *leaderboard.RankCache
}

// Option 配置 Tournament。
type Option func(*config)

type config struct {
	durationSec int64
	startDelay  int64
	rankCache   *leaderboard.RankCache // 可复用外部 RankCache;nil 则自建
}

// WithDuration 设置每周期时长(秒)。到期后自动滚动到下一 cron 周期。
// 默认 0:表示"按 cron 重置点滚动,不设独立结束时间"(单次榜)。
func WithDuration(sec int64) Option { return func(c *config) { c.durationSec = sec } }

// WithStartDelay 首周期启动延迟(秒)。用于"开赛时间晚于 cron 首点"的场景。
func WithStartDelay(sec int64) Option { return func(c *config) { c.startDelay = sec } }

// WithRankCache 复用外部 RankCache(多锦标赛共享一个缓存池)。
func WithRankCache(rc *leaderboard.RankCache) Option { return func(c *config) { c.rankCache = rc } }

// New 创建锦标赛。resetCron 是 5 字段 cron 表达式(分 时 日 月 周),
// 如 "0 9 * * *"=每天 9 点重置。durationSec>0 表示每周期时长。
func New(id string, order leaderboard.SortOrder, resetCron string, opts ...Option) (*Tournament, error) {
	cfg := &config{}
	for _, o := range opts {
		o(cfg)
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	sched, err := parser.Parse(resetCron)
	if err != nil {
		return nil, fmt.Errorf("tournament: parse cron %q: %w", resetCron, err)
	}
	rc := cfg.rankCache
	if rc == nil {
		rc = leaderboard.New()
	}
	return &Tournament{
		id:          id,
		order:       order,
		schedule:    sched,
		durationSec: cfg.durationSec,
		startDelay:  cfg.startDelay,
		rc:          rc,
	}, nil
}

// currentExpiry 计算当前时间所属周期的 expiry(下一次重置点)。
// 参考 Nakama calculateTournamentDeadlines:找"上一个 reset 点 + duration 仍覆盖 now"的周期。
func (t *Tournament) currentExpiry(now time.Time) int64 {
	next := t.schedule.Next(now) // 下一次重置
	if t.durationSec <= 0 {
		return next.Unix() // 单次榜:expiry = 下一个重置点
	}
	// 周期性:向前回溯找到覆盖 now 的周期。
	// 简化:expiry = next(下一重置点),当前周期 = [next-duration, next]。
	// 若 now 在 startDelay 之前,expiry 仍是 next(首周期未开始则算下一周期)。
	return next.Unix()
}

// CurrentExpiry 返回当前周期的 expiry(unix 秒),作为 leaderboard 的时间窗 key。
func (t *Tournament) CurrentExpiry() int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.currentExpiry(time.Now())
}

// Fill 全量加载当前周期的榜单记录。
func (t *Tournament) Fill(records []leaderboard.Record, enable bool) int {
	expiry := t.CurrentExpiry()
	return t.rc.Fill(t.id, expiry, t.order, records, enable)
}

// Insert 增量提交一条成绩,返回新名次。
func (t *Tournament) Insert(rec leaderboard.Record, enable bool) int64 {
	expiry := t.CurrentExpiry()
	return t.rc.Insert(t.id, expiry, t.order, rec, enable)
}

// Get 查某用户当前周期名次。
func (t *Tournament) Get(ownerID string) int64 {
	expiry := t.CurrentExpiry()
	return t.rc.Get(t.id, expiry, ownerID)
}

// TopN 取当前周期前 N 名。
func (t *Tournament) TopN(n int) []leaderboard.Record {
	expiry := t.CurrentExpiry()
	return t.rc.TopN(t.id, expiry, n)
}

// Around 取当前周期某用户附近 around 名(含自己)。
func (t *Tournament) Around(ownerID string, around int) []leaderboard.Record {
	expiry := t.CurrentExpiry()
	return t.rc.Around(t.id, expiry, ownerID, around)
}

// Size 当前周期记录数。
func (t *Tournament) Size() int {
	expiry := t.CurrentExpiry()
	return t.rc.Size(t.id, expiry)
}

// Delete 删除某用户当前周期记录。
func (t *Tournament) Delete(ownerID string) bool {
	expiry := t.CurrentExpiry()
	return t.rc.Delete(t.id, expiry, ownerID)
}

// NextReset 返回下一次重置时间。
func (t *Tournament) NextReset() time.Time {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.schedule.Next(time.Now())
}

// RankCache 返回底层 RankCache(供高级用法/多锦标赛共享)。
func (t *Tournament) RankCache() *leaderboard.RankCache { return t.rc }

// ErrInvalidCron cron 表达式无效。
var ErrInvalidCron = errors.New("tournament: invalid cron expression")
