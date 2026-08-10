package connectrpc

import (
	"net/http"
	"testing"
)

func TestParseServiceName(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/acme.user.v1.UserService/", "acme.user.v1.UserService"},
		{"/grpc.health.v1.Health/", "grpc.health.v1.Health"},
		{"/pkg.v1.Svc/", "pkg.v1.Svc"},
		{"/", ""},
		{"", ""},
		{"/healthz", ""},
		{"/api/v1/users", ""},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := parseServiceName(tt.path)
			if got != tt.want {
				t.Errorf("parseServiceName(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestNew(t *testing.T) {
	srv := New(":0")
	if srv.name != "connect-server" {
		t.Errorf("default name = %q, want %q", srv.name, "connect-server")
	}
	if !srv.enableH2C {
		t.Error("H2C should be enabled by default")
	}
	if !srv.enableHealth {
		t.Error("health check should be enabled by default")
	}
	if srv.Kind() != "connect" {
		t.Errorf("Kind() = %q, want %q", srv.Kind(), "connect")
	}
}

func TestNewWithOptions(t *testing.T) {
	srv := New(":0",
		WithServiceName("my-svc"),
		WithH2C(false),
		WithHealthCheck(false),
		WithVersion("v1.2.3"),
		WithWeight(200),
		WithEnvironment("staging"),
	)
	if srv.name != "my-svc" {
		t.Errorf("name = %q, want %q", srv.name, "my-svc")
	}
	if srv.enableH2C {
		t.Error("H2C should be disabled")
	}
	if srv.enableHealth {
		t.Error("health check should be disabled")
	}
	if srv.metadata["version"] != "v1.2.3" {
		t.Errorf("version = %q, want %q", srv.metadata["version"], "v1.2.3")
	}
	if srv.metadata["weight"] != "200" {
		t.Errorf("weight = %q, want %q", srv.metadata["weight"], "200")
	}
	if srv.metadata["environment"] != "staging" {
		t.Errorf("environment = %q, want %q", srv.metadata["environment"], "staging")
	}
}

func TestHandleTracksServiceNames(t *testing.T) {
	noop := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	srv := New(":0")
	srv.Handle("/acme.user.v1.UserService/", noop)
	srv.Handle("/acme.group.v1.GroupService/", noop)
	srv.HandleFunc("/healthz", noop)

	if len(srv.serviceNames) != 2 {
		t.Fatalf("serviceNames len = %d, want 2", len(srv.serviceNames))
	}
	if srv.serviceNames[0] != "acme.user.v1.UserService" {
		t.Errorf("serviceNames[0] = %q, want %q", srv.serviceNames[0], "acme.user.v1.UserService")
	}
	if srv.serviceNames[1] != "acme.group.v1.GroupService" {
		t.Errorf("serviceNames[1] = %q, want %q", srv.serviceNames[1], "acme.group.v1.GroupService")
	}
}
