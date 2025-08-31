package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"time"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/logger"
	"github.com/rushteam/beauty/pkg/middleware/circuitbreaker"
	"github.com/rushteam/beauty/pkg/middleware/timeout"
	"github.com/rushteam/beauty/pkg/service/grpcserver"
	"github.com/rushteam/beauty/pkg/service/webserver"
	"google.golang.org/grpc"
)

func main() {
	// 创建熔断器
	cb := circuitbreaker.NewCircuitBreaker(circuitbreaker.Config{
		Name:        "example-service",
		MaxRequests: 3,
		Interval:    10 * time.Second,
		Timeout:     5 * time.Second,
		ReadyToTrip: func(counts circuitbreaker.Counts) bool {
			return counts.Requests >= 5 &&
				float64(counts.TotalFailures)/float64(counts.Requests) > 0.6
		},
		OnStateChange: func(name string, from circuitbreaker.State, to circuitbreaker.State) {
			logger.Info("熔断器状态变化",
				"service", name,
				"from", from.String(),
				"to", to.String())
		},
	})

	// 创建超时控制器
	tc := timeout.NewTimeoutController(timeout.Config{
		Name:          "example-timeout",
		Timeout:       3 * time.Second,
		SlowThreshold: 1 * time.Second,
		EnableMetrics: true,
		OnTimeout: func(name string, duration time.Duration) {
			logger.Warn("请求超时", "service", name, "duration", duration.String())
		},
		OnSlow: func(name string, duration time.Duration) {
			logger.Warn("慢请求检测", "service", name, "duration", duration.String())
		},
	})

	// 创建 HTTP 路由
	mux := http.NewServeMux()

	// 添加状态监控端点
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		cbStats := cb.Counts()
		tcStats := tc.Stats()

		response := map[string]interface{}{
			"circuit_breaker": map[string]interface{}{
				"name":                 cb.Name(),
				"state":                cb.State().String(),
				"requests":             cbStats.Requests,
				"successes":            cbStats.TotalSuccesses,
				"failures":             cbStats.TotalFailures,
				"consecutive_failures": cbStats.ConsecutiveFailures,
			},
			"timeout_controller": map[string]interface{}{
				"name":             tc.Name(),
				"timeout":          tc.Timeout().String(),
				"total_requests":   tcStats.TotalRequests,
				"timeout_requests": tcStats.TimeoutRequests,
				"slow_requests":    tcStats.SlowRequests,
				"timeout_rate":     fmt.Sprintf("%.2f%%", tc.TimeoutRate()*100),
				"slow_rate":        fmt.Sprintf("%.2f%%", tc.SlowRate()*100),
				"avg_duration":     tcStats.AvgDuration.String(),
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	})

	// 重置统计信息端点
	mux.HandleFunc("/reset", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		cb.Reset()
		tc.ResetStats()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"message": "Statistics reset successfully"}`))
	})

	// 模拟不同类型的端点
	mux.HandleFunc("/fast", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(time.Duration(100+rand.Intn(200)) * time.Millisecond)
		w.Write([]byte("Fast response"))
	})

	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(time.Duration(1500+rand.Intn(1000)) * time.Millisecond)
		w.Write([]byte("Slow response"))
	})

	mux.HandleFunc("/timeout", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(time.Duration(4000+rand.Intn(2000)) * time.Millisecond)
		w.Write([]byte("This should timeout"))
	})

	mux.HandleFunc("/error", func(w http.ResponseWriter, r *http.Request) {
		if rand.Float64() < 0.7 { // 70% 失败率
			http.Error(w, "Simulated error", http.StatusInternalServerError)
			return
		}
		w.Write([]byte("Success response"))
	})

	// 自定义中间件示例
	loggingMiddleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			next.ServeHTTP(w, r)
			duration := time.Since(start)
			logger.Info("HTTP请求完成",
				"method", r.Method,
				"path", r.URL.Path,
				"duration", duration.String())
		})
	}

	// 创建应用 - 展示新的中间件组合方式
	app := beauty.New(
		// Web 服务器 - 同时使用多个中间件
		beauty.WithService(webserver.New(":8080", mux,
			webserver.WithServiceName("web-server"),
			// 中间件的执行顺序：loggingMiddleware -> 超时控制 -> 熔断器 -> 实际处理器
			webserver.WithMiddleware(loggingMiddleware), // 最外层：日志中间件
			webserver.WithTimeout(tc),                   // 中层：超时控制
			webserver.WithCircuitBreaker(cb),            // 最内层：熔断器
		)),

		// gRPC 服务器 - 同时使用多个拦截器
		beauty.WithService(grpcserver.New(":9090", func(s *grpc.Server) {
			// 这里可以注册 gRPC 服务
		},
			grpcserver.WithServiceName("grpc-server"),
			grpcserver.WithTimeout(tc),        // 超时控制拦截器
			grpcserver.WithCircuitBreaker(cb), // 熔断器拦截器
		)),
	)

	// 启动测试请求
	go func() {
		time.Sleep(2 * time.Second) // 等待服务启动

		logger.Info("开始发送测试请求...")

		endpoints := []string{"/fast", "/slow", "/timeout", "/error"}

		for i := 0; i < 50; i++ {
			endpoint := endpoints[rand.Intn(len(endpoints))]

			go func(i int, endpoint string) {
				client := &http.Client{
					Timeout: 5 * time.Second,
				}

				resp, err := client.Get("http://localhost:8080" + endpoint)
				if err != nil {
					logger.Error("HTTP请求失败", "error", err, "request", i, "endpoint", endpoint)
					return
				}
				defer resp.Body.Close()

				if resp.StatusCode >= 400 {
					logger.Warn("HTTP请求错误", "status", resp.StatusCode, "request", i, "endpoint", endpoint)
				} else {
					logger.Info("HTTP请求成功", "request", i, "endpoint", endpoint)
				}
			}(i, endpoint)

			time.Sleep(300 * time.Millisecond)
		}
	}()

	// 启动状态监控
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			cbStats := cb.Counts()
			tcStats := tc.Stats()

			logger.Info("中间件状态监控",
				"circuit_breaker_state", cb.State().String(),
				"cb_requests", cbStats.Requests,
				"cb_failures", cbStats.TotalFailures,
				"cb_consecutive_failures", cbStats.ConsecutiveFailures,
				"tc_total_requests", tcStats.TotalRequests,
				"tc_timeout_requests", tcStats.TimeoutRequests,
				"tc_slow_requests", tcStats.SlowRequests,
				"tc_timeout_rate", fmt.Sprintf("%.2f%%", tc.TimeoutRate()*100),
				"tc_avg_duration", tcStats.AvgDuration.String())
		}
	}()

	logger.Info("启动服务...")
	logger.Info("Web服务器: http://localhost:8080")
	logger.Info("gRPC服务器: localhost:9090")
	logger.Info("状态监控: http://localhost:8080/status")
	logger.Info("重置统计: POST http://localhost:8080/reset")
	logger.Info("测试端点:")
	logger.Info("  - 快速响应: http://localhost:8080/fast")
	logger.Info("  - 慢响应: http://localhost:8080/slow")
	logger.Info("  - 超时响应: http://localhost:8080/timeout")
	logger.Info("  - 错误响应: http://localhost:8080/error")

	if err := app.Start(context.Background()); err != nil {
		logger.Error("应用启动失败", "error", err)
	}
}
