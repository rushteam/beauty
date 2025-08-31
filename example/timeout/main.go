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
	"github.com/rushteam/beauty/pkg/timeout"
	"google.golang.org/grpc"
)

func main() {
	// 创建超时控制器配置
	timeoutConfig := timeout.Config{
		Name:          "example-timeout",
		Timeout:       5 * time.Second, // 5秒超时
		SlowThreshold: 2 * time.Second, // 2秒慢请求阈值
		EnableMetrics: true,
		OnTimeout: func(name string, duration time.Duration) {
			logger.Warn("请求超时",
				"service", name,
				"duration", duration.String())
		},
		OnSlow: func(name string, duration time.Duration) {
			logger.Warn("检测到慢请求",
				"service", name,
				"duration", duration.String())
		},
	}

	// 创建超时控制器
	tc := timeout.NewTimeoutController(timeoutConfig)

	// 创建 HTTP 多路复用器
	mux := http.NewServeMux()

	// 添加超时控制器状态查看端点
	mux.HandleFunc("/timeout/status", func(w http.ResponseWriter, r *http.Request) {
		stats := tc.Stats()

		response := map[string]interface{}{
			"name":             tc.Name(),
			"timeout":          tc.Timeout().String(),
			"slow_threshold":   tc.SlowThreshold().String(),
			"total_requests":   stats.TotalRequests,
			"timeout_requests": stats.TimeoutRequests,
			"slow_requests":    stats.SlowRequests,
			"timeout_rate":     fmt.Sprintf("%.2f%%", tc.TimeoutRate()*100),
			"slow_rate":        fmt.Sprintf("%.2f%%", tc.SlowRate()*100),
			"avg_duration":     stats.AvgDuration.String(),
			"max_duration":     stats.MaxDuration.String(),
			"min_duration":     stats.MinDuration.String(),
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	})

	// 添加重置统计信息端点
	mux.HandleFunc("/timeout/reset", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		tc.ResetStats()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"message": "Timeout stats reset successfully"}`))
	})

	// 模拟快速响应的端点
	mux.HandleFunc("/fast", func(w http.ResponseWriter, r *http.Request) {
		// 模拟快速处理（0.5-1秒）
		duration := time.Duration(500+rand.Intn(500)) * time.Millisecond
		time.Sleep(duration)
		w.Write([]byte(fmt.Sprintf("Fast response completed in %s", duration)))
	})

	// 模拟慢响应的端点
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		// 模拟慢处理（2-4秒）
		duration := time.Duration(2000+rand.Intn(2000)) * time.Millisecond
		time.Sleep(duration)
		w.Write([]byte(fmt.Sprintf("Slow response completed in %s", duration)))
	})

	// 模拟超时的端点
	mux.HandleFunc("/timeout", func(w http.ResponseWriter, r *http.Request) {
		// 模拟超时处理（6-10秒）
		duration := time.Duration(6000+rand.Intn(4000)) * time.Millisecond
		time.Sleep(duration)
		w.Write([]byte(fmt.Sprintf("This should timeout, but completed in %s", duration)))
	})

	// 模拟随机响应时间的端点
	mux.HandleFunc("/random", func(w http.ResponseWriter, r *http.Request) {
		// 随机响应时间（0.1-8秒）
		duration := time.Duration(100+rand.Intn(7900)) * time.Millisecond
		time.Sleep(duration)
		w.Write([]byte(fmt.Sprintf("Random response completed in %s", duration)))
	})

	// 测试超时控制的端点
	mux.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		// 在这个端点中，我们手动使用超时控制器
		err := tc.Execute(r.Context(), func(ctx context.Context) error {
			// 模拟一些处理时间
			processingTime := time.Duration(rand.Intn(8000)) * time.Millisecond

			select {
			case <-time.After(processingTime):
				// 正常完成
				return nil
			case <-ctx.Done():
				// 上下文被取消
				return ctx.Err()
			}
		})

		if err != nil {
			if err == timeout.ErrTimeout {
				http.Error(w, "Request timeout", http.StatusRequestTimeout)
				return
			}
			if err == timeout.ErrTimeoutCanceled {
				http.Error(w, "Request canceled", http.StatusRequestTimeout)
				return
			}
			http.Error(w, "Internal error", http.StatusInternalServerError)
			return
		}

		w.Write([]byte("Test request completed successfully"))
	})

	// 创建应用
	app := beauty.New(
		// 使用带超时控制的 Web 服务器
		beauty.WithWebServerTimeout(":8080", mux, tc,
			beauty.WithServiceName("web-server")),

		// 使用带超时控制的 gRPC 服务器（简单示例）
		beauty.WithGrpcServerTimeout(":9090", func(s *grpc.Server) {
			// 这里可以注册 gRPC 服务
		}, tc, beauty.WithServiceName("grpc-server")),
	)

	// 启动定时任务来模拟请求
	go func() {
		time.Sleep(2 * time.Second) // 等待服务启动

		logger.Info("开始模拟请求...")

		endpoints := []string{"/fast", "/slow", "/timeout", "/random", "/test"}

		for i := 0; i < 30; i++ {
			endpoint := endpoints[rand.Intn(len(endpoints))]

			go func(i int, endpoint string) {
				client := &http.Client{
					Timeout: 10 * time.Second, // 客户端超时设置得更长一些
				}

				resp, err := client.Get("http://localhost:8080" + endpoint)
				if err != nil {
					logger.Error("HTTP请求失败", "error", err, "request", i, "endpoint", endpoint)
					return
				}
				defer resp.Body.Close()

				if resp.StatusCode != http.StatusOK {
					logger.Warn("HTTP请求返回错误状态", "status", resp.StatusCode, "request", i, "endpoint", endpoint)
				} else {
					logger.Info("HTTP请求成功", "request", i, "endpoint", endpoint)
				}
			}(i, endpoint)

			time.Sleep(500 * time.Millisecond)
		}
	}()

	// 启动状态监控
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			stats := tc.Stats()
			logger.Info("超时控制器状态",
				"name", tc.Name(),
				"timeout", tc.Timeout().String(),
				"total_requests", stats.TotalRequests,
				"timeout_requests", stats.TimeoutRequests,
				"slow_requests", stats.SlowRequests,
				"timeout_rate", fmt.Sprintf("%.2f%%", tc.TimeoutRate()*100),
				"slow_rate", fmt.Sprintf("%.2f%%", tc.SlowRate()*100),
				"avg_duration", stats.AvgDuration.String())
		}
	}()

	// 启动全局超时管理器示例
	go func() {
		time.Sleep(3 * time.Second)

		// 使用全局超时管理器
		globalTC := timeout.GetTimeoutController("global-service", 3*time.Second)

		// 测试全局超时控制器
		for i := 0; i < 5; i++ {
			go func(i int) {
				err := globalTC.Execute(context.Background(), func(ctx context.Context) error {
					// 模拟处理时间
					processingTime := time.Duration(rand.Intn(5000)) * time.Millisecond
					time.Sleep(processingTime)
					return nil
				})

				if err != nil {
					logger.Error("全局超时控制器请求失败", "error", err, "request", i)
				} else {
					logger.Info("全局超时控制器请求成功", "request", i)
				}
			}(i)

			time.Sleep(1 * time.Second)
		}
	}()

	logger.Info("启动服务...")
	logger.Info("Web服务器: http://localhost:8080")
	logger.Info("gRPC服务器: localhost:9090")
	logger.Info("超时状态: http://localhost:8080/timeout/status")
	logger.Info("重置统计: POST http://localhost:8080/timeout/reset")
	logger.Info("测试端点:")
	logger.Info("  - 快速响应: http://localhost:8080/fast")
	logger.Info("  - 慢响应: http://localhost:8080/slow")
	logger.Info("  - 超时响应: http://localhost:8080/timeout")
	logger.Info("  - 随机响应: http://localhost:8080/random")
	logger.Info("  - 测试端点: http://localhost:8080/test")

	if err := app.Start(context.Background()); err != nil {
		logger.Error("应用启动失败", "error", err)
	}
}
