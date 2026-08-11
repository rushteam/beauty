package discover

import "testing"

func TestBuiltinCodecs(t *testing.T) {
	c, ok := GetCodec("beauty")
	if !ok {
		t.Fatal("beauty codec not registered")
	}
	if !c.Accept(ServiceInfo{Kind: "grpc"}) {
		t.Error("beauty codec should accept grpc")
	}
	if c.Accept(ServiceInfo{Kind: "http"}) {
		t.Error("beauty codec should reject http")
	}
}

func TestAcceptAllCodec(t *testing.T) {
	c, ok := GetCodec("accept_all")
	if !ok {
		t.Fatal("accept_all codec not registered")
	}
	if !c.Accept(ServiceInfo{Kind: "http"}) {
		t.Error("accept_all should accept anything")
	}
	if !c.Accept(ServiceInfo{}) {
		t.Error("accept_all should accept empty")
	}
}

func TestBuiltinKVCodec(t *testing.T) {
	c, ok := GetKVCodec("beauty")
	if !ok {
		t.Fatal("beauty kv codec not registered")
	}
	if got := c.BuildWatchPrefix("my-svc"); got != "/beauty/my-svc" {
		t.Errorf("unexpected prefix: %s", got)
	}
}

func TestRegisterAndGetCodec(t *testing.T) {
	RegisterCodec("test-custom", AcceptAllCodec())
	c, ok := GetCodec("test-custom")
	if !ok {
		t.Fatal("custom codec not found")
	}
	if !c.Accept(ServiceInfo{}) {
		t.Error("custom codec should accept")
	}
}

func TestGetCodec_NotFound(t *testing.T) {
	_, ok := GetCodec("nonexistent")
	if ok {
		t.Error("should not find nonexistent codec")
	}
}

func TestGetKVCodec_NotFound(t *testing.T) {
	_, ok := GetKVCodec("nonexistent")
	if ok {
		t.Error("should not find nonexistent kv codec")
	}
}
