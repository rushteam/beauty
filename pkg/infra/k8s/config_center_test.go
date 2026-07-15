package k8s_test

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakeclient "k8s.io/client-go/kubernetes/fake"

	beautyk8s "github.com/rushteam/beauty/pkg/infra/k8s"
)

// 用 client-go 的 fake clientset 验证 ConfigCenter 的 Get/Watch 接线。
// 局限:fake tracker 不评估 FieldSelector(生产由 apiserver 执行,按 name 只盯
// 目标资源),也不会在 Watch 建立时补发已存在对象的 ADDED——所以这里的 Watch
// 用例靠 Update 触发 MODIFIED 事件来验证,而非依赖初始 ADDED。

func cmObj(ns, name string, data map[string]string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Data:       data,
	}
}

func TestGet_ConfigMap(t *testing.T) {
	client := fakeclient.NewSimpleClientset(
		cmObj("prod", "app-config", map[string]string{"app.yaml": "port: 8080"}),
	)
	cc := beautyk8s.NewConfigMapCenter(client, "prod")

	got, err := cc.Get(context.Background(), "app-config/app.yaml")
	if err != nil {
		t.Fatalf("Get err = %v", err)
	}
	if got != "port: 8080" {
		t.Fatalf("Get = %q, want %q", got, "port: 8080")
	}
}

func TestGet_ConfigMap_BinaryData(t *testing.T) {
	client := fakeclient.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "bin"},
		BinaryData: map[string][]byte{"blob": []byte("raw-bytes")},
	})
	cc := beautyk8s.NewConfigMapCenter(client, "default")

	got, err := cc.Get(context.Background(), "bin/blob")
	if err != nil {
		t.Fatalf("Get err = %v", err)
	}
	if got != "raw-bytes" {
		t.Fatalf("Get = %q, want %q", got, "raw-bytes")
	}
}

func TestGet_MissingDataKey(t *testing.T) {
	client := fakeclient.NewSimpleClientset(
		cmObj("default", "app-config", map[string]string{"other.yaml": "x"}),
	)
	cc := beautyk8s.NewConfigMapCenter(client, "default")

	if _, err := cc.Get(context.Background(), "app-config/app.yaml"); err == nil {
		t.Fatal("Get missing dataKey should error")
	}
}

func TestGet_MissingResource(t *testing.T) {
	client := fakeclient.NewSimpleClientset()
	cc := beautyk8s.NewConfigMapCenter(client, "default")

	if _, err := cc.Get(context.Background(), "nope/app.yaml"); err == nil {
		t.Fatal("Get missing configmap should error")
	}
}

func TestGet_Secret_Decoded(t *testing.T) {
	client := fakeclient.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "db"},
		// client-go 的 Secret.Data 是已解码的原始字节(yaml 里才是 base64)。
		Data: map[string][]byte{"password": []byte("s3cr3t")},
	})
	cc := beautyk8s.NewSecretCenter(client, "default")

	got, err := cc.Get(context.Background(), "db/password")
	if err != nil {
		t.Fatalf("Get err = %v", err)
	}
	if got != "s3cr3t" {
		t.Fatalf("Get = %q, want %q", got, "s3cr3t")
	}
}

func TestGet_BadKeyFormat(t *testing.T) {
	cc := beautyk8s.NewConfigMapCenter(fakeclient.NewSimpleClientset(), "default")
	for _, bad := range []string{"noslash", "/leading", "trailing/"} {
		if _, err := cc.Get(context.Background(), bad); err == nil {
			t.Fatalf("Get(%q) should error on bad key format", bad)
		}
	}
}

func TestWatch_ConfigMapUpdate(t *testing.T) {
	client := fakeclient.NewSimpleClientset(
		cmObj("default", "app-config", map[string]string{"app.yaml": "v1"}),
	)
	cc := beautyk8s.NewConfigMapCenter(client, "default")

	ctx := t.Context() // 测试结束时自动取消,停止 watch goroutine

	changes := make(chan string, 8)
	if _, err := cc.Watch(ctx, "app-config/app.yaml", func(_, value string) {
		changes <- value
	}); err != nil {
		t.Fatalf("Watch err = %v", err)
	}

	// 让 watch goroutine 先注册到 fake tracker,再做更新触发 MODIFIED。
	waitWatchReady(t, changes, client)

	update := func(v string) {
		_, err := client.CoreV1().ConfigMaps("default").Update(
			ctx, cmObj("default", "app-config", map[string]string{"app.yaml": v}), metav1.UpdateOptions{})
		if err != nil {
			t.Fatalf("update err = %v", err)
		}
	}

	update("v2")
	if got := recv(t, changes); got != "v2" {
		t.Fatalf("change = %q, want v2", got)
	}

	// 值未变的更新应被去重,不再回调。
	update("v2")
	// 值变化应回调。
	update("v3")
	if got := recv(t, changes); got != "v3" {
		t.Fatalf("after dedup, change = %q, want v3 (v2->v2 should have been deduped)", got)
	}
}

// waitWatchReady 反复更新一个探针值直到 Watch 回调开始工作,吸收掉这些探针回调,
// 消除 "Watch goroutine 尚未注册到 fake tracker 就执行 Update" 的竞态。
func waitWatchReady(t *testing.T, changes <-chan string, client *fakeclient.Clientset) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	probe := 0
	for {
		select {
		case <-changes:
			// watch 已就绪并推来了探针值;把通道里残留的探针回调排空。
			drain(changes)
			return
		case <-tick.C:
			probe++
			_, _ = client.CoreV1().ConfigMaps("default").Update(
				context.Background(),
				cmObj("default", "app-config", map[string]string{"app.yaml": "probe", "n": itoa(probe)}),
				metav1.UpdateOptions{})
		case <-deadline:
			t.Fatal("watch never became ready")
		}
	}
}

func drain(ch <-chan string) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

func recv(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for change")
		return ""
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
