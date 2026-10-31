package etcdv3

import (
	"testing"

	"github.com/rushteam/beauty/pkg/service/discover"
	"go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"
)

func putEv(t *testing.T, path string, s discover.ServiceInfo) *clientv3.Event {
	t.Helper()
	v, err := s.Marshal()
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	return &clientv3.Event{
		Type: clientv3.EventTypePut,
		Kv:   &mvccpb.KeyValue{Key: []byte(path + "/" + s.ID), Value: []byte(v)},
	}
}

func delEv(path, id string) *clientv3.Event {
	return &clientv3.Event{
		Type: clientv3.EventTypeDelete,
		Kv:   &mvccpb.KeyValue{Key: []byte(path + "/" + id)},
	}
}

func TestApplyEvents_Batch(t *testing.T) {
	path := "/beauty/svc"
	reg := &Registry{codec: discover.NewBeautyKVCodec("beauty")}
	endpoints := map[string]discover.ServiceInfo{
		"old": {ID: "old", Name: "svc", Kind: "grpc", Addr: "10.0.0.1:1"},
	}

	// 同一 WatchResponse 内的滚动发布：下线旧实例 + 上线两个新实例
	changed := reg.applyEvents(endpoints, "svc", path, []*clientv3.Event{
		delEv(path, "old"),
		putEv(t, path, discover.ServiceInfo{ID: "n1", Name: "svc", Kind: "grpc", Addr: "10.0.0.2:1"}),
		putEv(t, path, discover.ServiceInfo{ID: "n2", Name: "svc", Kind: "grpc", Addr: "10.0.0.3:1"}),
	})
	if !changed {
		t.Fatal("batch should report changed")
	}
	got := buildSortedServices(endpoints)
	if len(got) != 2 || got[0].ID != "n1" || got[1].ID != "n2" {
		t.Fatalf("unexpected endpoints: %v", got)
	}
}

// nameFromArgCodec 模拟 value 中不带服务名、依赖 UnmarshalValue 入参补全 Name 的自定义 codec。
type nameFromArgCodec struct{ discover.KVCodec }

func (c nameFromArgCodec) UnmarshalValue(val []byte, serviceName string) (discover.ServiceInfo, error) {
	v, err := c.KVCodec.UnmarshalValue(val, serviceName)
	v.Name = serviceName
	return v, err
}

// Watch 路径传给 codec 的必须是服务名（与 Find 一致），而不是 watch 前缀。
func TestApplyEvents_PassesServiceNameToCodec(t *testing.T) {
	path := "/beauty/svc"
	reg := &Registry{codec: nameFromArgCodec{discover.NewBeautyKVCodec("beauty")}}
	endpoints := map[string]discover.ServiceInfo{}
	reg.applyEvents(endpoints, "svc", path, []*clientv3.Event{
		putEv(t, path, discover.ServiceInfo{ID: "1", Kind: "grpc", Addr: "10.0.0.1:1"}),
	})
	if got := endpoints["1"].Name; got != "svc" {
		t.Fatalf("Name = %q, want %q", got, "svc")
	}
}

func TestApplyEvents_NoopNotChanged(t *testing.T) {
	path := "/beauty/svc"
	reg := &Registry{codec: discover.NewBeautyKVCodec("beauty")}
	cur := discover.ServiceInfo{ID: "1", Name: "svc", Kind: "grpc", Addr: "10.0.0.1:1", Metadata: map[string]string{"zone": "a"}}
	endpoints := map[string]discover.ServiceInfo{}
	if !reg.applyEvents(endpoints, "svc", path, []*clientv3.Event{putEv(t, path, cur)}) {
		t.Fatal("first put should report changed")
	}

	cases := map[string][]*clientv3.Event{
		"identical re-put":    {putEv(t, path, cur)},
		"delete unknown":      {delEv(path, "missing")},
		"filtered kind":       {putEv(t, path, discover.ServiceInfo{ID: "2", Name: "svc", Kind: "http"})},
		"bad value":           {{Type: clientv3.EventTypePut, Kv: &mvccpb.KeyValue{Key: []byte(path + "/3"), Value: []byte("not-json")}}},
		"progress (no event)": nil,
	}
	for name, evs := range cases {
		if reg.applyEvents(endpoints, "svc", path, evs) {
			t.Errorf("%s: should not report changed", name)
		}
	}

	changedMeta := cur
	changedMeta.Metadata = map[string]string{"zone": "b"}
	if !reg.applyEvents(endpoints, "svc", path, []*clientv3.Event{putEv(t, path, changedMeta)}) {
		t.Fatal("metadata change should report changed")
	}
}
