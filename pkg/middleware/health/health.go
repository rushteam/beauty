package health

import (
	"encoding/json"
	"net/http"
)

type ReadinessCheck func() error

type handler struct {
	checks []ReadinessCheck
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/healthz":
		writeLiveness(w)
	case "/readyz":
		h.writeReadiness(w)
	default:
		http.NotFound(w, r)
	}
}

func writeLiveness(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *handler) writeReadiness(w http.ResponseWriter) {
	for _, check := range h.checks {
		if err := check(); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"status": "error", "error": err.Error()})
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func Handler(checks ...ReadinessCheck) http.Handler {
	return &handler{checks: checks}
}

func Middleware(checks ...ReadinessCheck) func(http.Handler) http.Handler {
	h := &handler{checks: checks}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/healthz":
				writeLiveness(w)
			case "/readyz":
				h.writeReadiness(w)
			default:
				next.ServeHTTP(w, r)
			}
		})
	}
}
