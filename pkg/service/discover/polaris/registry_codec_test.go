package polaris

import (
	"net/url"
	"testing"

	"github.com/polarismesh/polaris-go/pkg/model"
)

type fakeInstance struct {
	model.Instance
	id, kind string
}

func (f fakeInstance) IsHealthy() bool                { return true }
func (f fakeInstance) IsIsolated() bool               { return false }
func (f fakeInstance) GetWeight() int                 { return 100 }
func (f fakeInstance) GetId() string                  { return f.id }
func (f fakeInstance) GetService() string             { return "payment" }
func (f fakeInstance) GetHost() string                { return "10.0.0.1" }
func (f fakeInstance) GetPort() uint32                { return 8080 }
func (f fakeInstance) GetMetadata() map[string]string { return map[string]string{"kind": f.kind} }

func TestBuildServiceInfos_UsesCodec(t *testing.T) {
	instances := []model.Instance{fakeInstance{id: "g", kind: "grpc"}, fakeInstance{id: "h", kind: "http"}}

	def := NewRegistry(&Config{})
	if got := buildServiceInfos(instances, def.effectiveCodec()); len(got) != 1 || got[0].ID != "g" {
		t.Fatalf("default codec should keep only grpc, got %v", got)
	}

	u, _ := url.Parse("polaris://127.0.0.1:8091/default?codec=accept_all")
	r, err := NewFromURL(*u)
	if err != nil {
		t.Fatal(err)
	}
	if got := buildServiceInfos(instances, r.effectiveCodec()); len(got) != 2 {
		t.Fatalf("accept_all codec should keep http instances, got %v", got)
	}
	if s := r.c.String(); s != "polaris://127.0.0.1:8091/default?codec=accept_all" {
		t.Fatalf("String() = %q", s)
	}
}
