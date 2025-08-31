package etcdv3

import (
	"reflect"
	"testing"

	"github.com/rushteam/beauty/pkg/service/discover"
)

func TestBuildSortedServices(t *testing.T) {
	endpoints := map[string]discover.ServiceInfo{
		"b": {ID: "b", Name: "svc"},
		"a": {ID: "a", Name: "svc"},
		"c": {ID: "c", Name: "svc"},
	}
	got := buildSortedServices(endpoints)
	want := []discover.ServiceInfo{{ID: "a", Name: "svc"}, {ID: "b", Name: "svc"}, {ID: "c", Name: "svc"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("want %v, got %v", want, got)
	}
}

func TestApplyPutEvent_FilterGrpc(t *testing.T) {
	path := "/beauty/svc"
	reg := &Registry{}
	endpoints := map[string]discover.ServiceInfo{}

	// grpc
	vGrpc := discover.ServiceInfo{ID: "1", Name: "svc", Kind: "grpc"}
	services := reg.applyPutEvent(endpoints, path, path+"/1", []byte(vGrpc.Marshal()))
	if len(services) != 1 || services[0].ID != "1" {
		t.Fatalf("grpc add failed: %v", services)
	}

	// non-grpc
	vNon := discover.ServiceInfo{ID: "2", Name: "svc", Kind: "http"}
	services = reg.applyPutEvent(endpoints, path, path+"/2", []byte(vNon.Marshal()))
	if len(services) != 1 || services[0].ID != "1" {
		t.Fatalf("non-grpc should be filtered, got: %v", services)
	}

	// change existing to non-grpc -> remove
	vGrpc.Kind = "http"
	services = reg.applyPutEvent(endpoints, path, path+"/1", []byte(vGrpc.Marshal()))
	if len(services) != 0 {
		t.Fatalf("change to non-grpc should remove, got: %v", services)
	}
}

func TestApplyDeleteEvent(t *testing.T) {
	path := "/beauty/svc"
	reg := &Registry{}
	endpoints := map[string]discover.ServiceInfo{
		"1": {ID: "1", Name: "svc", Kind: "grpc"},
		"2": {ID: "2", Name: "svc", Kind: "grpc"},
	}
	services := reg.applyDeleteEvent(endpoints, path, path+"/1")
	if len(services) != 1 || services[0].ID != "2" {
		t.Fatalf("delete failed, got: %v", services)
	}
}
