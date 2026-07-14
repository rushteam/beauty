// Package k8s 提供基于 Kubernetes 原生选主能力实现 pkg/dlock.Elector 的后端,
// 使 Cron 等"多实例只该有一个在跑"的场景可以直接用集群内已有的 k8s API,
// 不必额外运维一套 etcd/Redis。
//
// 实现是 k8s.io/client-go/tools/leaderelection 的薄封装(不重新发明选主算法):
// 用 coordination.k8s.io/v1 的 Lease 资源作为选主载体(k8s 控制面组件如
// kube-controller-manager 用的就是同一套机制),clientset 复用
// pkg/service/discover/k8s 已有的 in-cluster/kubeconfig 判定模式。
//
// 只实现 Elector,不实现 Locker——client-go 的 leaderelection 是"选主"语义
// (持续参选、当选后运行、失去续期能力则让位),不提供通用的一次性互斥锁原语,
// 如实反映这个限制而非用选主拼凑一个语义不符的 Lock/Unlock。
package k8s

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	coordinationv1client "k8s.io/client-go/kubernetes/typed/coordination/v1"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rushteam/beauty/pkg/dlock"
)

// Elector 基于 k8s Lease 资源实现 dlock.Elector。零值不可用,用 NewElector 构造。
type Elector struct {
	client    coordinationv1client.CoordinationV1Interface
	namespace string
	identity  string

	leaseDuration time.Duration
	renewDeadline time.Duration
	retryPeriod   time.Duration
}

// Option 配置 Elector。
type Option func(*Elector)

// WithNamespace 设置 Lease 资源所在的命名空间(默认 "default")。
func WithNamespace(ns string) Option {
	return func(e *Elector) {
		if ns != "" {
			e.namespace = ns
		}
	}
}

// WithIdentity 设置本实例的参选身份标识(默认取 Pod hostname / os.Hostname())。
// 同一选举内必须唯一,否则多个"自己"会互相当作同一个候选人。
func WithIdentity(id string) Option {
	return func(e *Elector) {
		if id != "" {
			e.identity = id
		}
	}
}

// WithTiming 设置选举时序参数:leaseDuration 是非 leader 等多久才可强制抢占
// (默认 15s);renewDeadline 是 leader 续期的最大重试时长(默认 10s,须小于
// leaseDuration);retryPeriod 是候选者的重试间隔(默认 2s)。这组默认值与
// k8s 核心组件(kube-scheduler 等)一致。
func WithTiming(leaseDuration, renewDeadline, retryPeriod time.Duration) Option {
	return func(e *Elector) {
		if leaseDuration > 0 {
			e.leaseDuration = leaseDuration
		}
		if renewDeadline > 0 {
			e.renewDeadline = renewDeadline
		}
		if retryPeriod > 0 {
			e.retryPeriod = retryPeriod
		}
	}
}

// NewElector 用已有的 CoordinationV1 客户端创建 Elector(便于测试时传入 fake
// clientset)。生产用法自行构造 clientset 后取其 CoordinationV1() 传入即可,
// in-cluster/kubeconfig 的判定模式可参考 pkg/service/discover/k8s 里已有的实现。
func NewElector(client coordinationv1client.CoordinationV1Interface, opts ...Option) *Elector {
	hostname, _ := os.Hostname()
	e := &Elector{
		client:        client,
		namespace:     "default",
		identity:      hostname,
		leaseDuration: 15 * time.Second,
		renewDeadline: 10 * time.Second,
		retryPeriod:   2 * time.Second,
	}
	for _, o := range opts {
		o(e)
	}
	return e
}

// leaseLock 为 key 构造一个 LeaseLock(每个 key 对应集群内独立的 Lease 资源)。
func (e *Elector) leaseLock(key string) *resourcelock.LeaseLock {
	return &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{Name: key, Namespace: e.namespace},
		Client:    e.client,
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: e.identity,
		},
	}
}

// Run 实现 dlock.Elector:参选 key(对应命名空间下同名的 Lease 资源),当选时
// 以"续期存活期间"为 leaderCtx 调用 onElected。leaderelection.LeaderElector.Run
// 本身只跑一轮选举(当选→续期直到失败或 ctx 取消就返回),这里外面包一层循环,
// 在 outer ctx 未取消时重新参选,直到 outer ctx 取消才真正退出。
func (e *Elector) Run(ctx context.Context, key string, onElected func(leaderCtx context.Context)) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := e.electOnce(ctx, key, onElected); err != nil {
			return err
		}
	}
}

func (e *Elector) electOnce(ctx context.Context, key string, onElected func(leaderCtx context.Context)) error {
	lock := e.leaseLock(key)

	// client-go 用 `go Callbacks.OnStartedLeading(ctx)` 异步调用当选回调,Run()
	// 不等它跑完就可能返回;OnStoppedLeading 是同步的、且"退出前必调用一次"
	// (即便从未当选),用它兜底同步,让 electOnce 尽量等 onElected 跑完才返回,
	// 缩小与下一轮选举重叠的窗口。
	//
	// 已知残余竞态(client-go 设计使然,无法从外部完全封死):OnStartedLeading 的
	// goroutine 何时真正开始执行没有硬性时序保证,理论上 OnStoppedLeading 可能
	// 在它启动前就已触发,导致本函数早退、下一轮选举提前开始。即便命中,client-go
	// 会在 Run 返回时 cancel 传给 OnStartedLeading 的 ctx,所以迟启动的 onElected
	// 拿到的 leaderCtx 已经(或即将)Done——只要 onElected 遵守"必须检查
	// leaderCtx.Done()"的约定(dlock.Elector 接口的硬性要求),就不会有两边同时
	// 认为自己是 leader 而做实际工作的后果,至多是一次几乎立即返回的空跑。
	done := make(chan struct{})
	var closeDone sync.Once
	markDone := func() { closeDone.Do(func() { close(done) }) }
	le, err := leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
		Lock:            lock,
		LeaseDuration:   e.leaseDuration,
		RenewDeadline:   e.renewDeadline,
		RetryPeriod:     e.retryPeriod,
		ReleaseOnCancel: true, // outer ctx 取消时主动放弃,加速下个实例接管
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(leaderCtx context.Context) {
				defer markDone()
				onElected(leaderCtx)
			},
			// OnStoppedLeading 同步触发于 Run 返回前,但 OnStartedLeading 是否已经
			// 真正跑起来没有硬性时序保证(见上方注释)——可能先于 OnStartedLeading
			// 的 goroutine 被调度就已触发。两者都可能 markDone,用 sync.Once 保证
			// 只关闭一次(此前用 select-default 判断再 close 不是原子操作,存在
			// "两边都判断到未关闭、都执行 close"从而 panic 的竞态,已修正)。
			OnStoppedLeading: markDone,
		},
	})
	if err != nil {
		return fmt.Errorf("k8s dlock: new leader elector: %w", err)
	}
	le.Run(ctx)
	<-done // 等 onElected(若发生过)确认已经返回,避免与下一轮选举重叠
	return nil
}

var _ dlock.Elector = (*Elector)(nil)
