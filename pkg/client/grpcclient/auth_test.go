package grpcclient

import (
	"context"
	"testing"
)

func TestBearerToken_GetRequestMetadata(t *testing.T) {
	bt := BearerToken("my-token")
	md, err := bt.GetRequestMetadata(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := md["authorization"]; got != "Bearer my-token" {
		t.Fatalf("got %q", got)
	}
}

func TestBearerToken_RequireTransportSecurity(t *testing.T) {
	bt := BearerToken("tok")
	if bt.RequireTransportSecurity() {
		t.Fatal("should not require transport security")
	}
}

func TestHasScheme(t *testing.T) {
	tests := []struct {
		target string
		want   bool
	}{
		{"k8s://svc.ns", true},
		{"etcd://127.0.0.1:2379/svc", true},
		{"nacos://host:8848/svc", true},
		{"127.0.0.1:8080", false},
		{"localhost:50051", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := hasScheme(tt.target); got != tt.want {
			t.Errorf("hasScheme(%q) = %v, want %v", tt.target, got, tt.want)
		}
	}
}
