package resty_test

import (
	"net/http"
	"testing"

	resty "github.com/rushteam/beauty/pkg/client/http"
)

func TestWithBaseTransport(t *testing.T) {
	var hit bool
	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		hit = true
		return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody, Header: make(http.Header)}, nil
	})
	client := resty.NewHTTPClient(resty.WithBaseTransport(base), resty.WithTimeout(0))
	req, err := http.NewRequest(http.MethodGet, "http://example.invalid/ping", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !hit || resp.StatusCode != http.StatusNoContent {
		t.Fatalf("hit=%v status=%d", hit, resp.StatusCode)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
