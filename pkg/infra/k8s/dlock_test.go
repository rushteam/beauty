package k8s_test

import (
	"context"
	"testing"
	"time"

	fakeclient "k8s.io/client-go/kubernetes/fake"

	beautyk8s "github.com/rushteam/beauty/pkg/infra/k8s"
)

// 本包用 client-go 的 fake clientset 验证：Elector 正确调用 leaderelection 的
// 参数与生命周期回调,不依赖真实 k8s API server。这不是端到端集成测试(fake
// clientset 不模拟真实的乐观锁冲突/网络分区/API server 行为),只验证薄封装本身
// 的接线正确——与 pkg/infra/etcd 那一档"真实 etcd 集成测试"验证强度不同,如实标注。

func newFakeElector(opts ...beautyk8s.Option) *beautyk8s.Elector {
	client := fakeclient.NewSimpleClientset()
	return beautyk8s.NewElector(client.CoordinationV1(), opts...)
}

func TestRun_BecomesLeaderAndInvokesCallback(t *testing.T) {
	e := newFakeElector(
		beautyk8s.WithIdentity("pod-a"),
		beautyk8s.WithTiming(300*time.Millisecond, 200*time.Millisecond, 20*time.Millisecond),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	elected := make(chan struct{})
	go e.Run(ctx, "test-lease", func(leaderCtx context.Context) {
		close(elected)
		<-leaderCtx.Done()
	})

	select {
	case <-elected:
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("onElected was never called against fake clientset")
	}
}

func TestRun_LeaderCtxCancelledOnOuterCancel(t *testing.T) {
	e := newFakeElector(
		beautyk8s.WithIdentity("pod-a"),
		beautyk8s.WithTiming(300*time.Millisecond, 200*time.Millisecond, 20*time.Millisecond),
	)

	ctx, cancel := context.WithCancel(context.Background())
	elected := make(chan context.Context, 1)
	done := make(chan error, 1)
	go func() {
		done <- e.Run(ctx, "test-lease-2", func(leaderCtx context.Context) {
			elected <- leaderCtx
			<-leaderCtx.Done()
		})
	}()

	var leaderCtx context.Context
	select {
	case leaderCtx = <-elected:
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("never elected")
	}

	cancel() // outer ctx 取消

	select {
	case <-leaderCtx.Done():
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("leaderCtx should be cancelled after outer ctx cancel")
	}

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("Run err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after outer ctx cancel")
	}
}

// 没有"多 Elector 竞选同一 Lease、验证互斥"的测试:实测发现 client-go 的
// fake clientset 底层 tracker 不做 resourceVersion 乐观锁仲裁(试过让 3 个
// Elector 竞选同一 key,3 个全部"成功"当选),伪造不出真实的互斥效果。
// 真正的互斥语义来自 k8s API server 对 Lease 更新的 resourceVersion 冲突检测,
// fake clientset 结构上验证不了这一点——用它硬凑一个"互斥"断言只会给出
// 虚假的安全感,所以如实地不做这个测试,把它留给真实集群验证。

func TestWithNamespace_Default(t *testing.T) {
	client := fakeclient.NewSimpleClientset()
	e := beautyk8s.NewElector(client.CoordinationV1())
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	// 不显式设置 namespace 时应能正常工作(默认 "default"),不 panic。
	e.Run(ctx, "ns-default-test", func(leaderCtx context.Context) { <-leaderCtx.Done() })
}
