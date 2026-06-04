package errors

import (
	"encoding/json"
	"net/http"
)

// httpResponse 是写给客户端的 JSON 结构。
// cause 字段永远不序列化，确保内部错误不泄露。
type httpResponse struct {
	Code    int           `json:"code"`
	Message string        `json:"message"`
	Details []detailJSON  `json:"details,omitempty"`
}

type detailJSON struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

// WriteHTTP 将 *Status 序列化为 JSON 写入 http.ResponseWriter。
// Content-Type 固定为 application/json。
func WriteHTTP(w http.ResponseWriter, s *Status) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(s.code.HTTPStatus())
	resp := httpResponse{
		Code:    int(s.code),
		Message: s.message,
	}
	for _, d := range s.details {
		resp.Details = append(resp.Details, detailJSON{
			Type: d.detailType(),
			Data: d,
		})
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// HTTPMiddlewareErrorHandler 返回一个 HTTP 中间件，将 handler 产生的 *Status 错误
// 转换为结构化 JSON 响应。handler 通过 ctx 中的 errorSink 写入错误。
//
// 使用场景：handler 无法直接返回 error（net/http 签名限制），
// 可通过 SetError(ctx, err) 写入，中间件统一处理。
//
//	func MyHandler(w http.ResponseWriter, r *http.Request) {
//	    user, err := svc.GetUser(r.Context(), id)
//	    if err != nil {
//	        errors.SetError(r.Context(), err)
//	        return
//	    }
//	    json.NewEncoder(w).Encode(user)
//	}
func HTTPMiddlewareErrorHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := withErrorSink(r.Context())
		next.ServeHTTP(w, r.WithContext(ctx))
		if err := getError(ctx); err != nil {
			if s, ok := FromError(err); ok {
				WriteHTTP(w, s)
			} else {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(httpResponse{
					Code:    int(CodeInternal),
					Message: "internal server error",
				})
			}
		}
	})
}
