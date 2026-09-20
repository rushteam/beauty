package scandefender

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDefender_Admit_BelowRate(t *testing.T) {
	d := New(WithConnRate(3, time.Minute))
	for i := 0; i < 3; i++ {
		if err := d.Admit("1.2.3.4:1000"); err != nil {
			t.Fatalf("连接 #%d 被拒绝: %v", i+1, err)
		}
	}
}

func TestDefender_Admit_ExceedRate(t *testing.T) {
	d := New(WithConnRate(2, time.Minute))
	_ = d.Admit("1.2.3.4:1000")
	_ = d.Admit("1.2.3.4:1001")
	err := d.Admit("1.2.3.4:1002")
	if err != ErrBanned {
		t.Fatalf("超频第3次应被封禁, got %v", err)
	}
	// 封禁后再次连接也拒绝
	if err := d.Admit("1.2.3.4:1003"); err != ErrBanned {
		t.Fatal("封禁期内应持续拒绝")
	}
}

func TestDefender_FlashDisconnect(t *testing.T) {
	d := New(
		WithConnRate(100, time.Minute),
		WithFlashThreshold(2, 500*time.Millisecond),
		WithFlashWindow(time.Minute),
	)
	addr := "10.0.0.1:5000"
	now := time.Now()

	// 两次闪断(连接持续 < 500ms)
	_ = d.Admit(addr)
	d.OnClose(addr, now.Add(-100*time.Millisecond))
	_ = d.Admit(addr)
	d.OnClose(addr, now.Add(-50*time.Millisecond))

	// 第三次闪断触发封禁
	_ = d.Admit(addr)
	d.OnClose(addr, now.Add(-10*time.Millisecond))

	if !d.IsBanned(addr) {
		t.Fatal("多次闪断后应被封禁")
	}
}

func TestDefender_NormalClose_NoFlash(t *testing.T) {
	d := New(
		WithConnRate(100, time.Minute),
		WithFlashThreshold(2, 500*time.Millisecond),
	)
	addr := "10.0.0.2:6000"
	now := time.Now()

	// 连接持续 > 500ms,不算闪断
	for i := 0; i < 10; i++ {
		_ = d.Admit(addr)
		d.OnClose(addr, now.Add(-time.Second))
	}
	if d.IsBanned(addr) {
		t.Fatal("正常断开不应被封禁")
	}
}

func TestDefender_DifferentIPs_Isolated(t *testing.T) {
	d := New(WithConnRate(2, time.Minute))
	_ = d.Admit("1.1.1.1:1000")
	_ = d.Admit("1.1.1.1:1001")
	_ = d.Admit("1.1.1.1:1002") // 封禁

	// 另一个 IP 不受影响
	if err := d.Admit("2.2.2.2:1000"); err != nil {
		t.Fatalf("不同 IP 不应互相影响: %v", err)
	}
}

func TestDefender_Unban(t *testing.T) {
	d := New(WithConnRate(1, time.Minute))
	_ = d.Admit("3.3.3.3:1000")
	_ = d.Admit("3.3.3.3:1001") // 封禁

	if !d.IsBanned("3.3.3.3:1001") {
		t.Fatal("应被封禁")
	}
	d.Unban("3.3.3.3")
	if d.IsBanned("3.3.3.3") {
		t.Fatal("解封后不应被封禁")
	}
}

func TestDefender_Cleanup(t *testing.T) {
	d := New(WithConnRate(100, time.Minute))
	_ = d.Admit("4.4.4.4:1000")

	d.mu.Lock()
	// 手动让条目过期
	st := d.ips["4.4.4.4"]
	st.conns = []time.Time{time.Now().Add(-2 * time.Hour)}
	d.mu.Unlock()

	d.Cleanup()

	d.mu.Lock()
	_, exists := d.ips["4.4.4.4"]
	d.mu.Unlock()
	if exists {
		t.Fatal("过期条目应被清理")
	}
}

func TestDefender_ExtractIP(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"1.2.3.4:8080", "1.2.3.4"},
		{"[::1]:443", "::1"},
		{"1.2.3.4", "1.2.3.4"},
	}
	for _, tt := range tests {
		got := extractIP(tt.in)
		if got != tt.want {
			t.Errorf("extractIP(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestDefender_Middleware(t *testing.T) {
	d := New(WithConnRate(1, time.Minute))

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := d.Middleware(inner)

	// 第一次请求放行
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "5.5.5.5:9000"
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("第一次应放行, got %d", rec.Code)
	}

	// 第二次触发封禁
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.RemoteAddr = "5.5.5.5:9001"
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("封禁后应返回 403, got %d", rec2.Code)
	}
}

func TestDefender_OnHandshake_ResetCounter(t *testing.T) {
	d := New(WithConnRate(3, time.Minute))
	addr := "10.10.10.10:1000"

	// 连3次,刚好达到阈值
	_ = d.Admit(addr)
	_ = d.Admit(addr)
	_ = d.Admit(addr)

	// 握手成功,重置计数器
	d.OnHandshake(addr)

	// 重置后应该又能连接
	if err := d.Admit(addr); err != nil {
		t.Fatalf("握手重置后应放行, got %v", err)
	}
}

func TestDefender_OnHandshake_PreventsFalsePositive(t *testing.T) {
	// 模拟公司出口 IP:多人连接 + 握手成功,不应被封
	d := New(WithConnRate(3, time.Minute))
	companyIP := "192.168.1.1"

	for i := 0; i < 10; i++ {
		addr := companyIP + ":5000"
		err := d.Admit(addr)
		if err == ErrBanned {
			t.Fatalf("第%d次连接被封, 不应该(有合法握手重置)", i+1)
		}
		// 每次连接后都成功握手,重置计数器
		d.OnHandshake(addr)
	}
}

func TestDefender_OnHandshake_UnknownIP(t *testing.T) {
	// 对不存在的 IP 调用 OnHandshake 不 panic
	d := New()
	d.OnHandshake("1.1.1.1:9999") // should not panic
}
