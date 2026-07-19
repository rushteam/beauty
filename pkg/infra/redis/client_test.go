package redis

import "testing"

func TestOptions(t *testing.T) {
	var cfg clientConfig
	WithTracing()(&cfg)
	WithMetrics()(&cfg)
	if !cfg.tracing || !cfg.metrics {
		t.Fatalf("tracing=%v metrics=%v, want both true", cfg.tracing, cfg.metrics)
	}
}

// 接入埋点的客户端应能正常构造(懒连接,不 Ping),instrument 不报错/不 panic。
func TestNewClient_Instrumented(t *testing.T) {
	c := NewClient(&Config{Addr: "127.0.0.1:6399", PoolSize: 5, DialTimeout: 0}, WithTracing(), WithMetrics())
	if c == nil {
		t.Fatal("client 不应为 nil")
	}
	_ = c.Close()
}
