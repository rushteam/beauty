package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"time"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/logger"
	"github.com/rushteam/beauty/pkg/middleware/circuitbreaker"
	"github.com/rushteam/beauty/pkg/service/grpcserver"
	"github.com/rushteam/beauty/pkg/service/webserver"
	"google.golang.org/grpc"
)

func main() {
	// 创建熔断器配置
	cbConfig := circuitbreaker.NewCustomConfig("example-service").
		WithMaxRequests(3).             // 半开状态下最大请求数
		WithInterval(10 * time.Second). // 统计窗口
		WithTimeout(5 * time.Second).   // 熔断超时时间
		WithReadyToTrip(func(counts circuitbreaker.Counts) bool {
			// 当请求数超过5且失败率超过60%时触发熔断
			return counts.Requests >= 5 &&
				float64(counts.TotalFailures)/float64(counts.Requests) > 0.6
		}).
		WithOnStateChange(func(name string, from circuitbreaker.State, to circuitbreaker.State) {
			logger.Info("熔断器状态变化",
				"service", name,
				"from", from.String(),
				"to", to.String())
		}).
		Build()

	// 创建熔断器
	cb := circuitbreaker.NewCircuitBreaker(cbConfig)

	// 创建 HTTP 多路复用器
	mux := http.NewServeMux()

	// 添加熔断器状态查看端点
	mux.HandleFunc("/circuit-breaker/status", func(w http.ResponseWriter, r *http.Request) {
		stats := cb.Counts()
		state := cb.State()

		response := map[string]interface{}{
			"name":                  cb.Name(),
			"state":                 state.String(),
			"requests":              stats.Requests,
			"successes":             stats.TotalSuccesses,
			"failures":              stats.TotalFailures,
			"consecutive_successes": stats.ConsecutiveSuccesses,
			"consecutive_failures":  stats.ConsecutiveFailures,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	})

	// 添加重置熔断器端点
	mux.HandleFunc("/circuit-breaker/reset", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		cb.Reset()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"message": "Circuit breaker reset successfully"}`))
	})

	// 模拟不稳定的 HTTP 端点
	mux.HandleFunc("/unstable", func(w http.ResponseWriter, r *http.Request) {
		// 模拟50%的失败率
		if rand.Float64() < 0.5 {
			http.Error(w, "Service temporarily unavailable", http.StatusInternalServerError)
			return
		}

		// 模拟处理时间
		time.Sleep(time.Millisecond * time.Duration(rand.Intn(100)))
		w.Write([]byte("Service is working fine!"))
	})

	// 测试熔断器的端点
	mux.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		err := cb.Call(func() error {
			// 模拟调用不稳定的服务
			if rand.Float64() < 0.7 { // 70%的失败率
				return errors.New("service error")
			}
			return nil
		})

		if err != nil {
			if err == circuitbreaker.ErrCircuitBreakerOpen || err == circuitbreaker.ErrTooManyRequests {
				http.Error(w, fmt.Sprintf("Circuit breaker: %s", err.Error()), http.StatusServiceUnavailable)
				return
			}
			http.Error(w, "Service error", http.StatusInternalServerError)
			return
		}

		w.Write([]byte("Request successful!"))
	})

	// 创建应用
	app := beauty.New(
		// 使用带熔断器的 Web 服务器
		beauty.WithService(webserver.New(":8080", mux,
			webserver.WithServiceName("web-server"),
			webserver.WithCircuitBreaker(cb),
		)),

		// 使用带熔断器的 gRPC 服务器（简单示例，不注册具体服务）
		beauty.WithService(grpcserver.New(":9090", func(s *grpc.Server) {
			// 这里可以注册 gRPC 服务
		},
			grpcserver.WithServiceName("grpc-server"),
			grpcserver.WithCircuitBreaker(cb),
		)),
	)

	// 启动定时任务来模拟请求
	go func() {
		time.Sleep(2 * time.Second) // 等待服务启动

		logger.Info("开始模拟请求...")

		for i := 0; i < 50; i++ {
			// 模拟 HTTP 请求
			go func(i int) {
				resp, err := http.Get("http://localhost:8080/test")
				if err != nil {
					logger.Error("HTTP请求失败", "error", err, "request", i)
					return
				}
				defer resp.Body.Close()

				if resp.StatusCode != http.StatusOK {
					logger.Warn("HTTP请求返回错误状态", "status", resp.StatusCode, "request", i)
				} else {
					logger.Info("HTTP请求成功", "request", i)
				}
			}(i)

			time.Sleep(500 * time.Millisecond)
		}
	}()

	// 启动状态监控
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				stats := cb.Counts()
				state := cb.State()
				logger.Info("熔断器状态",
					"name", cb.Name(),
					"state", state.String(),
					"requests", stats.Requests,
					"successes", stats.TotalSuccesses,
					"failures", stats.TotalFailures,
					"consecutive_failures", stats.ConsecutiveFailures)
			}
		}
	}()

	logger.Info("启动服务...")
	logger.Info("Web服务器: http://localhost:8080")
	logger.Info("gRPC服务器: localhost:9090")
	logger.Info("熔断器状态: http://localhost:8080/circuit-breaker/status")
	logger.Info("重置熔断器: POST http://localhost:8080/circuit-breaker/reset")
	logger.Info("测试端点: http://localhost:8080/test")

	if err := app.Start(context.Background()); err != nil {
		logger.Error("应用启动失败", "error", err)
	}
}
