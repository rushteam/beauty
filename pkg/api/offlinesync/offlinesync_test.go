package offlinesync_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/rushteam/beauty/pkg/api/offlinesync"
)

func TestHandler_BatchIdempotent(t *testing.T) {
	var n atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/orders", func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"order":"o1"}`))
	})

	h := offlinesync.New(mux)
	body := []byte(`{"ops":[{"id":"op-1","method":"POST","path":"/api/orders","body":{"sku":"a"}}]}`)

	do := func() offlinesync.Response {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/sync", bytes.NewReader(body))
		h.ServeHTTP(rec, req)
		var resp offlinesync.Response
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatal(err)
		}
		return resp
	}

	r1 := do()
	r2 := do()
	if n.Load() != 1 {
		t.Fatalf("executor called %d, want 1", n.Load())
	}
	if r1.Results[0].Status != 201 || r2.Results[0].Status != 201 {
		t.Fatalf("status %d / %d", r1.Results[0].Status, r2.Results[0].Status)
	}
	if r1.Results[0].Replayed {
		t.Fatal("first should not be replayed")
	}
	if !r2.Results[0].Replayed {
		t.Fatal("second should be replayed")
	}
}
