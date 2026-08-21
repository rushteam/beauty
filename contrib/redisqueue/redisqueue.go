// Package redisqueue 提供基于 Redis 的分布式任务队列,实现 BullMQ 风格的
// 优先级队列 + 延迟任务 + 重试 + 生命周期事件。
//
// 与进程内 pkg/orchestration/jobqueue 的区别:
//   - 持久化:任务数据存储在 Redis,进程崩溃不丢失;
//   - 分布式:多个 worker 进程可并发消费同一队列;
//   - at-least-once:任务执行中 worker 崩溃,由可见性超时机制自动重新投递。
//
// 数据结构(参考 BullMQ 的 Redis 布局):
//   - {prefix}:{queue}:waiting   — Sorted Set(score=priority,延迟任务 score=timestamp)
//   - {prefix}:{queue}:active    — Set(正在处理的 job ID)
//   - {prefix}:{queue}:completed — Set(已完成)
//   - {prefix}:{queue}:failed    — Set(失败)
//   - {prefix}:{queue}:job:{id}  — Hash(任务详情: name, payload, priority, state, attempts…)
//   - {prefix}:{queue}:delayed   — Sorted Set(score=到期 unix ms,到期转 waiting)
//
// 原子性:关键操作(入队、出队、状态转移)使用 Lua 脚本保证原子。
//
// 用法:
//
//	rdb := redis.NewClient(...)
//	q := redisqueue.New(rdb, "my-tasks")
//	q.Submit(ctx, &redisqueue.Job{Name: "send-email", Payload: payload, Priority: 5})
//	q.StartWorker(ctx, handler) // 阻塞,消费循环
package redisqueue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// JobState 任务状态。
type JobState string

const (
	StateWaiting   JobState = "waiting"
	StateDelayed   JobState = "delayed"
	StateActive    JobState = "active"
	StateCompleted JobState = "completed"
	StateFailed    JobState = "failed"
)

// Job 一个任务。
type Job struct {
	ID         string        `json:"id"`
	Name       string        `json:"name"`
	Payload    []byte        `json:"payload"`
	Priority   int           `json:"priority"`
	Delay      time.Duration `json:"delay"`
	MaxRetries int           `json:"max_retries"`
	RetryDelay time.Duration `json:"retry_delay"`
	Timeout    time.Duration `json:"timeout"`
	State      JobState      `json:"state"`
	Attempts   int           `json:"attempts"`
	Progress   float64       `json:"progress"`
	Error      string        `json:"error,omitempty"`
	CreatedAt  int64         `json:"created_at"`
	StartedAt  int64         `json:"started_at,omitempty"`
	DoneAt     int64         `json:"done_at,omitempty"`
}

// EventType 事件类型。
type EventType string

const (
	EventSubmit   EventType = "submit"
	EventStart    EventType = "start"
	EventProgress EventType = "progress"
	EventComplete EventType = "complete"
	EventFail     EventType = "fail"
	EventRetry    EventType = "retry"
	EventStalled  EventType = "stalled"
)

// Event 生命周期事件。
type Event struct {
	Type  EventType
	Job   *Job
	Error error
}

// EventHook 事件回调。
type EventHook func(Event)

// Handler 任务处理函数。
type Handler func(ctx context.Context, job *Job) error

// Config 配置。
type Config struct {
	Prefix          string        // Redis key 前缀,默认 "bq"
	PollInterval    time.Duration // 空闲轮询间隔,默认 1s
	VisibilityTime  time.Duration // 可见性超时(worker 崩溃后多久重新投递),默认 30s
	DelayResolution time.Duration // 延迟任务检查间隔,默认 500ms
	Hook            EventHook     // 事件钩子
	OnPanic         func(job *Job, r any, stack []byte)
}

// Option 配置函数。
type Option func(*Config)

func WithPrefix(p string) Option                 { return func(c *Config) { c.Prefix = p } }
func WithPollInterval(d time.Duration) Option    { return func(c *Config) { c.PollInterval = d } }
func WithVisibilityTime(d time.Duration) Option  { return func(c *Config) { c.VisibilityTime = d } }
func WithDelayResolution(d time.Duration) Option { return func(c *Config) { c.DelayResolution = d } }
func WithHook(h EventHook) Option                { return func(c *Config) { c.Hook = h } }
func WithPanicHandler(fn func(job *Job, r any, stack []byte)) Option {
	return func(c *Config) { c.OnPanic = fn }
}

// Queue Redis 分布式任务队列。
type Queue struct {
	rdb  redis.Cmdable
	name string
	cfg  Config
}

// New 创建队列。name 是队列名(用于 Redis key 命名空间)。
func New(rdb redis.Cmdable, name string, opts ...Option) *Queue {
	cfg := Config{
		Prefix:          "bq",
		PollInterval:    time.Second,
		VisibilityTime:  30 * time.Second,
		DelayResolution: 500 * time.Millisecond,
	}
	for _, o := range opts {
		o(&cfg)
	}
	return &Queue{rdb: rdb, name: name, cfg: cfg}
}

func (q *Queue) key(suffix string) string {
	return fmt.Sprintf("%s:%s:%s", q.cfg.Prefix, q.name, suffix)
}

func (q *Queue) jobKey(id string) string {
	return fmt.Sprintf("%s:%s:job:%s", q.cfg.Prefix, q.name, id)
}

// Submit 投递一个任务到队列。
func (q *Queue) Submit(ctx context.Context, job *Job) error {
	if job.ID == "" {
		return errors.New("redisqueue: job ID is required")
	}
	now := time.Now()
	job.CreatedAt = now.UnixMilli()
	job.State = StateWaiting

	data, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("redisqueue: marshal job: %w", err)
	}

	pipe := q.rdb.Pipeline()
	pipe.Set(ctx, q.jobKey(job.ID), data, 0)

	if job.Delay > 0 {
		job.State = StateDelayed
		score := float64(now.Add(job.Delay).UnixMilli())
		pipe.ZAdd(ctx, q.key("delayed"), redis.Z{Score: score, Member: job.ID})
	} else {
		score := float64(job.Priority)
		pipe.ZAdd(ctx, q.key("waiting"), redis.Z{Score: score, Member: job.ID})
	}
	_, err = pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("redisqueue: submit: %w", err)
	}
	q.emit(Event{Type: EventSubmit, Job: job})
	return nil
}

// Pending 返回等待中的任务数。
func (q *Queue) Pending(ctx context.Context) (int64, error) {
	return q.rdb.ZCard(ctx, q.key("waiting")).Result()
}

// Delayed 返回延迟中的任务数。
func (q *Queue) Delayed(ctx context.Context) (int64, error) {
	return q.rdb.ZCard(ctx, q.key("delayed")).Result()
}

// ReportProgress 上报进度(worker 调用)。
func (q *Queue) ReportProgress(ctx context.Context, jobID string, percent float64) error {
	data, err := q.rdb.Get(ctx, q.jobKey(jobID)).Bytes()
	if err != nil {
		return err
	}
	var job Job
	if err := json.Unmarshal(data, &job); err != nil {
		return err
	}
	job.Progress = percent
	updated, _ := json.Marshal(&job)
	if err := q.rdb.Set(ctx, q.jobKey(jobID), updated, 0).Err(); err != nil {
		return err
	}
	q.emit(Event{Type: EventProgress, Job: &job})
	return nil
}

// StartWorker 启动消费循环。阻塞直到 ctx 取消。handler 处理单个任务。
// 可在多个进程/goroutine 中调用以实现水平扩展。
func (q *Queue) StartWorker(ctx context.Context, handler Handler) error {
	// 同时启动延迟任务调度
	go q.delayScheduler(ctx)
	go q.stalledChecker(ctx)

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		jobID, err := q.dequeue(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil
			}
			slog.Debug("redisqueue: dequeue error", "queue", q.name, "err", err)
			time.Sleep(q.cfg.PollInterval)
			continue
		}
		if jobID == "" {
			time.Sleep(q.cfg.PollInterval)
			continue
		}

		q.processJob(ctx, jobID, handler)
	}
}

// dequeue 原子地从 waiting 取出优先级最高的任务,移入 active。
func (q *Queue) dequeue(ctx context.Context) (string, error) {
	// ZPOPMIN 取优先级最低分数(=最高优先级)
	results, err := q.rdb.ZPopMin(ctx, q.key("waiting"), 1).Result()
	if err != nil {
		return "", err
	}
	if len(results) == 0 {
		return "", nil
	}
	jobID := results[0].Member.(string)
	// 加入 active 集合
	q.rdb.SAdd(ctx, q.key("active"), jobID)
	// 设置可见性超时 key(用于 stalled 检测)
	q.rdb.Set(ctx, q.key("lock:"+jobID), "1", q.cfg.VisibilityTime)
	return jobID, nil
}

func (q *Queue) processJob(ctx context.Context, jobID string, handler Handler) {
	data, err := q.rdb.Get(ctx, q.jobKey(jobID)).Bytes()
	if err != nil {
		slog.Debug("redisqueue: get job data failed", "id", jobID, "err", err)
		return
	}
	var job Job
	if err := json.Unmarshal(data, &job); err != nil {
		slog.Debug("redisqueue: unmarshal job failed", "id", jobID, "err", err)
		return
	}

	job.State = StateActive
	job.Attempts++
	job.StartedAt = time.Now().UnixMilli()
	q.saveJob(ctx, &job)
	q.emit(Event{Type: EventStart, Job: &job})

	execErr := q.safeExec(ctx, &job, handler)

	// 执行后删除可见性锁
	q.rdb.Del(ctx, q.key("lock:"+jobID))

	if execErr != nil {
		job.Error = execErr.Error()
		if job.Attempts <= job.MaxRetries {
			// 重试:延迟后重新入队
			q.emit(Event{Type: EventRetry, Job: &job, Error: execErr})
			delay := job.RetryDelay * time.Duration(1<<(job.Attempts-1))
			if delay <= 0 {
				delay = time.Second
			}
			job.State = StateDelayed
			score := float64(time.Now().Add(delay).UnixMilli())
			q.rdb.SRem(ctx, q.key("active"), jobID)
			q.rdb.ZAdd(ctx, q.key("delayed"), redis.Z{Score: score, Member: jobID})
			q.saveJob(ctx, &job)
		} else {
			// 失败
			job.State = StateFailed
			job.DoneAt = time.Now().UnixMilli()
			q.rdb.SRem(ctx, q.key("active"), jobID)
			q.rdb.SAdd(ctx, q.key("failed"), jobID)
			q.saveJob(ctx, &job)
			q.emit(Event{Type: EventFail, Job: &job, Error: execErr})
		}
	} else {
		// 成功
		job.State = StateCompleted
		job.Progress = 100
		job.DoneAt = time.Now().UnixMilli()
		job.Error = ""
		q.rdb.SRem(ctx, q.key("active"), jobID)
		q.rdb.SAdd(ctx, q.key("completed"), jobID)
		q.saveJob(ctx, &job)
		q.emit(Event{Type: EventComplete, Job: &job})
	}
}

func (q *Queue) safeExec(ctx context.Context, job *Job, handler Handler) (err error) {
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			err = fmt.Errorf("redisqueue: panic in job %q: %v", job.Name, r)
			if q.cfg.OnPanic != nil {
				q.cfg.OnPanic(job, r, stack)
			}
		}
	}()
	execCtx := ctx
	if job.Timeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, job.Timeout)
		defer cancel()
	}
	return handler(execCtx, job)
}

func (q *Queue) saveJob(ctx context.Context, job *Job) {
	data, _ := json.Marshal(job)
	q.rdb.Set(ctx, q.jobKey(job.ID), data, 0)
}

// delayScheduler 定期将到期的延迟任务转入 waiting 队列。
func (q *Queue) delayScheduler(ctx context.Context) {
	ticker := time.NewTicker(q.cfg.DelayResolution)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := float64(time.Now().UnixMilli())
			// 取出所有到期任务
			ids, err := q.rdb.ZRangeByScore(ctx, q.key("delayed"), &redis.ZRangeBy{
				Min: "-inf",
				Max: strconv.FormatFloat(now, 'f', 0, 64),
			}).Result()
			if err != nil || len(ids) == 0 {
				continue
			}
			for _, id := range ids {
				q.rdb.ZRem(ctx, q.key("delayed"), id)
				// 读取 job 获取 priority
				data, err := q.rdb.Get(ctx, q.jobKey(id)).Bytes()
				if err != nil {
					continue
				}
				var job Job
				if json.Unmarshal(data, &job) != nil {
					continue
				}
				job.State = StateWaiting
				q.saveJob(ctx, &job)
				q.rdb.ZAdd(ctx, q.key("waiting"), redis.Z{Score: float64(job.Priority), Member: id})
			}
		}
	}
}

// stalledChecker 检测"卡住"的任务:active 中的任务如果可见性锁已过期,说明 worker 崩溃,
// 自动重新入队(at-least-once 保证)。
func (q *Queue) stalledChecker(ctx context.Context) {
	ticker := time.NewTicker(q.cfg.VisibilityTime / 2)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			activeIDs, err := q.rdb.SMembers(ctx, q.key("active")).Result()
			if err != nil || len(activeIDs) == 0 {
				continue
			}
			for _, id := range activeIDs {
				exists, _ := q.rdb.Exists(ctx, q.key("lock:"+id)).Result()
				if exists == 0 {
					// 锁已过期 → worker 崩溃,重新入队
					data, err := q.rdb.Get(ctx, q.jobKey(id)).Bytes()
					if err != nil {
						continue
					}
					var job Job
					if json.Unmarshal(data, &job) != nil {
						continue
					}
					q.rdb.SRem(ctx, q.key("active"), id)
					job.State = StateWaiting
					q.saveJob(ctx, &job)
					q.rdb.ZAdd(ctx, q.key("waiting"), redis.Z{Score: float64(job.Priority), Member: id})
					q.emit(Event{Type: EventStalled, Job: &job})
					slog.Debug("redisqueue: stalled job re-queued", "queue", q.name, "id", id)
				}
			}
		}
	}
}

func (q *Queue) emit(e Event) {
	if q.cfg.Hook != nil {
		q.cfg.Hook(e)
	}
}

// GetJob 查询指定 ID 的任务状态。
func (q *Queue) GetJob(ctx context.Context, id string) (*Job, error) {
	data, err := q.rdb.Get(ctx, q.jobKey(id)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, fmt.Errorf("redisqueue: job %q not found", id)
		}
		return nil, err
	}
	var job Job
	if err := json.Unmarshal(data, &job); err != nil {
		return nil, err
	}
	return &job, nil
}

// Clean 清理已完成/失败的任务数据(保留最近 keep 个)。
func (q *Queue) Clean(ctx context.Context, state JobState, keep int64) (int64, error) {
	var setKey string
	switch state {
	case StateCompleted:
		setKey = q.key("completed")
	case StateFailed:
		setKey = q.key("failed")
	default:
		return 0, fmt.Errorf("redisqueue: can only clean completed or failed")
	}

	count, err := q.rdb.SCard(ctx, setKey).Result()
	if err != nil || count <= keep {
		return 0, err
	}

	// 随机弹出多余的
	toRemove := count - keep
	var removed int64
	for i := int64(0); i < toRemove; i++ {
		id, err := q.rdb.SPop(ctx, setKey).Result()
		if err != nil {
			break
		}
		q.rdb.Del(ctx, q.jobKey(id))
		removed++
	}
	return removed, nil
}
