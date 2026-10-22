package webpush_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rushteam/beauty/contrib/webpush"
)

func TestMemStore_Lifecycle(t *testing.T) {
	s := webpush.NewMemStore()
	sub := webpush.Subscription{
		Endpoint: "https://push.example/x",
		Keys:     webpush.Keys{P256dh: "a", Auth: "b"},
	}
	if err := s.Save(context.Background(), "u1", sub); err != nil {
		t.Fatal(err)
	}
	list, err := s.List(context.Background(), "u1")
	if err != nil || len(list) != 1 {
		t.Fatalf("list=%v err=%v", list, err)
	}
	_ = s.Delete(context.Background(), sub.Endpoint)
	list, _ = s.List(context.Background(), "u1")
	if len(list) != 0 {
		t.Fatalf("want empty, got %d", len(list))
	}
}

func TestGenerateVAPIDKeys(t *testing.T) {
	k, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	if k.Public == "" || k.Private == "" {
		t.Fatal("empty keys")
	}
	c := webpush.NewClient(k, "mailto:test@example.com")
	if c.PublicKey() != k.Public {
		t.Fatal("public key mismatch")
	}
}

func TestSubscribeHandler(t *testing.T) {
	store := webpush.NewMemStore()
	h := webpush.SubscribeHandler(store, nil)

	body, _ := json.Marshal(webpush.Subscription{
		Endpoint: "https://push.example/1",
		Keys:     webpush.Keys{P256dh: "p", Auth: "a"},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/push/subscribe?user=alice", bytes.NewReader(body))
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code=%d", rec.Code)
	}
	list, _ := store.List(context.Background(), "alice")
	if len(list) != 1 {
		t.Fatalf("want 1 sub, got %d", len(list))
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/push/subscribe?endpoint=https://push.example/1", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete code=%d", rec.Code)
	}
}
