package handlers

import (
	"encoding/json"
	"net/http"
	"time"
)

// Response 统一响应格式
type Response struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
	Time    int64       `json:"time"`
}

// Success 成功响应
func Success(w http.ResponseWriter, data interface{}) {
	response := Response{
		Code:    200,
		Message: "success",
		Data:    data,
		Time:    time.Now().Unix(),
	}
	
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// Error 错误响应
func Error(w http.ResponseWriter, code int, message string) {
	response := Response{
		Code:    code,
		Message: message,
		Time:    time.Now().Unix(),
	}
	
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(response)
}

// Home 首页处理器
func Home(w http.ResponseWriter, r *http.Request) {
	Success(w, map[string]string{
		"service": "{{.Name}}",
		"version": "1.0.0",
		"message": "Welcome to {{.Name}} API",
	})
}

// Ping 健康检查处理器
func Ping(w http.ResponseWriter, r *http.Request) {
	Success(w, map[string]string{
		"status": "ok",
		"time":   time.Now().Format(time.RFC3339),
	})
}

// HealthCheck 健康检查
func HealthCheck(w http.ResponseWriter, r *http.Request) {
	Success(w, map[string]interface{}{
		"status":    "healthy",
		"timestamp": time.Now().Unix(),
		"uptime":    time.Since(time.Now()).String(),
	})
}

// Metrics 指标端点
func Metrics(w http.ResponseWriter, r *http.Request) {
	// 这里可以集成Prometheus指标
	Success(w, map[string]interface{}{
		"metrics": "prometheus",
		"status":  "available",
	})
}
