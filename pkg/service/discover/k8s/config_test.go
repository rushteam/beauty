package k8s

import (
	"net/url"
	"testing"
)

func mustParse(rawURL string) url.URL {
	u, err := url.Parse(rawURL)
	if err != nil {
		panic(err)
	}
	return *u
}

func TestNewFromURL_ServiceDotNamespace(t *testing.T) {
	c, err := NewFromURL(mustParse("k8s://payment-internal.mall?port_name=grpc&service_type=All"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Namespace != "mall" {
		t.Fatalf("Namespace = %q, want %q", c.Namespace, "mall")
	}
	if c.PortName != "grpc" {
		t.Fatalf("PortName = %q, want %q", c.PortName, "grpc")
	}
	if c.ServiceType != "All" {
		t.Fatalf("ServiceType = %q, want %q", c.ServiceType, "All")
	}
}

func TestNewFromURL_FullDNS(t *testing.T) {
	c, err := NewFromURL(mustParse("k8s://my-svc.kube-system.svc.cluster.local?port_name=http"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Namespace != "kube-system" {
		t.Fatalf("Namespace = %q, want %q", c.Namespace, "kube-system")
	}
	if c.PortName != "http" {
		t.Fatalf("PortName = %q, want %q", c.PortName, "http")
	}
}

func TestNewFromURL_BareService_DefaultNamespace(t *testing.T) {
	// 省略 namespace → default（与 K8s DNS 语义一致）
	c, err := NewFromURL(mustParse("k8s://payment-internal?port_name=grpc"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Namespace != "default" {
		t.Fatalf("Namespace = %q, want %q", c.Namespace, "default")
	}
}

func TestNewFromURL_Defaults(t *testing.T) {
	c, err := NewFromURL(mustParse("k8s://"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Namespace != "default" {
		t.Fatalf("Namespace = %q, want %q", c.Namespace, "default")
	}
	if c.ServiceType != "ClusterIP" {
		t.Fatalf("ServiceType = %q, want %q", c.ServiceType, "ClusterIP")
	}
	if c.WatchTimeout != 30 {
		t.Fatalf("WatchTimeout = %d, want 30", c.WatchTimeout)
	}
}

func TestNewFromURL_QueryOverridesNamespace(t *testing.T) {
	c, err := NewFromURL(mustParse("k8s://payment.mall?namespace=production"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Namespace != "production" {
		t.Fatalf("Namespace = %q, want %q (query should override)", c.Namespace, "production")
	}
}

// ---- normalizeServiceName ----

func TestNewFromURL_WildcardNamespace(t *testing.T) {
	// k8s://*.mall → namespace=mall，serviceName 为通配
	c, err := NewFromURL(mustParse("k8s://*.mall?label_selector=team%3Dpayment"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Namespace != "mall" {
		t.Fatalf("Namespace = %q, want %q", c.Namespace, "mall")
	}
}

func TestNewFromURL_QueryOnlyNamespace(t *testing.T) {
	// k8s://?namespace=mall → 纯 query 指定 namespace
	c, err := NewFromURL(mustParse("k8s://?namespace=mall&label_selector=team%3Dpayment"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Namespace != "mall" {
		t.Fatalf("Namespace = %q, want %q", c.Namespace, "mall")
	}
}

func TestNormalizeServiceName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"payment-internal", "payment-internal"},
		{"payment-internal.mall", "payment-internal"},
		{"my-svc.kube-system.svc.cluster.local", "my-svc"},
		{"*.mall", ""},
		{"*", ""},
		{".mall", ""},
		{"", ""},
	}
	for _, tt := range tests {
		got := normalizeServiceName(tt.input)
		if got != tt.want {
			t.Errorf("normalizeServiceName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
