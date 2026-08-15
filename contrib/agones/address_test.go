package agones

import (
	"testing"

	allocation "agones.dev/agones/pkg/allocation/go"
)

func TestFormatGameServerAddress(t *testing.T) {
	ports := []*allocation.AllocationResponse_GameServerStatusPort{
		{Name: "default", Port: 7654},
	}

	tests := []struct {
		host string
		want string
	}{
		{"10.0.0.1", "10.0.0.1:7654"},
		{"10.0.0.1:7654", "10.0.0.1:7654"},
		{"2001:db8::1", "[2001:db8::1]:7654"},
		{"[2001:db8::1]:7654", "[2001:db8::1]:7654"},
	}
	for _, tc := range tests {
		got := formatGameServerAddress(tc.host, ports)
		if got != tc.want {
			t.Fatalf("formatGameServerAddress(%q) = %q want %q", tc.host, got, tc.want)
		}
	}
}

func TestPickGamePort(t *testing.T) {
	ports := []*allocation.AllocationResponse_GameServerStatusPort{
		{Name: "metrics", Port: 8080},
		{Name: "default", Port: 7654},
	}
	if got := pickGamePort(ports); got != 7654 {
		t.Fatalf("pickGamePort = %d want 7654", got)
	}
}
