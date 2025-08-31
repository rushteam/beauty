package nacos

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/nacos-group/nacos-sdk-go/v2/model"
	"github.com/rushteam/beauty/pkg/service/discover"
)

func TestBuildService_FilterAndSort(t *testing.T) {
	instances := []model.Instance{
		{InstanceId: "2", ServiceName: "svc", Ip: "127.0.0.1", Port: 8080, Enable: true, Healthy: true, Weight: 1, Metadata: map[string]string{"kind": "grpc"}},
		{InstanceId: "1", ServiceName: "svc", Ip: "127.0.0.1", Port: 8081, Enable: true, Healthy: true, Weight: 1, Metadata: map[string]string{"kind": "grpc"}},
		{InstanceId: "3", ServiceName: "svc", Ip: "127.0.0.1", Port: 8082, Enable: true, Healthy: false, Weight: 1, Metadata: map[string]string{"kind": "grpc"}},
		{InstanceId: "4", ServiceName: "svc", Ip: "127.0.0.1", Port: 8083, Enable: true, Healthy: true, Weight: 0, Metadata: map[string]string{"kind": "grpc"}},
		{InstanceId: "5", ServiceName: "svc", Ip: "127.0.0.1", Port: 8084, Enable: true, Healthy: true, Weight: 1, Metadata: map[string]string{"kind": "http"}},
	}

	got := buildService(instances)
	want := []discover.ServiceInfo{
		{ID: "1", Name: "svc", Addr: fmt.Sprintf("%s:%d", "127.0.0.1", 8081), Metadata: map[string]string{"kind": "grpc"}},
		{ID: "2", Name: "svc", Addr: fmt.Sprintf("%s:%d", "127.0.0.1", 8080), Metadata: map[string]string{"kind": "grpc"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("want %v, got %v", want, got)
	}
}
