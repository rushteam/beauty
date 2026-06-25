// Package http 是 Ring 3（入站适配器）：把 HTTP 请求翻译成用例调用。
package http

import (
	"encoding/json"
	"log/slog"
	"net/http"

	useruc "{{.ImportPath}}internal/application/user"
)

// UserHandler 用户 HTTP 适配器
type UserHandler struct {
	svc *useruc.Service
}

// NewUserHandler 创建用户 handler
func NewUserHandler(svc *useruc.Service) *UserHandler {
	return &UserHandler{svc: svc}
}

// Register 把路由注册到给定的 mux（Go 1.22+ 方法路由）
func (h *UserHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/users", h.create)
	mux.HandleFunc("GET /api/v1/users", h.list)
	mux.HandleFunc("GET /api/v1/users/{id}", h.get)
}

type createUserRequest struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

func (h *UserHandler) create(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	u, err := h.svc.Create(r.Context(), req.Name, req.Email)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	// 使用 *Context 方法记录日志，自动带上 trace_id/span_id
	slog.InfoContext(r.Context(), "user created", "id", u.ID)
	writeJSON(w, http.StatusCreated, u)
}

func (h *UserHandler) get(w http.ResponseWriter, r *http.Request) {
	u, err := h.svc.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (h *UserHandler) list(w http.ResponseWriter, r *http.Request) {
	users, err := h.svc.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}
