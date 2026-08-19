package dlock_test

import (
	"testing"

	"github.com/rushteam/beauty/pkg/store/dlock"
)

func TestNewElectorAuto_NoK8s(t *testing.T) {
	elector, err := dlock.NewElectorAuto()
	if err != nil {
		t.Fatal(err)
	}
	if elector == nil {
		t.Fatal("expected non-nil elector")
	}
	if _, ok := elector.(*dlock.Memory); !ok {
		t.Fatalf("expected *Memory in non-k8s env, got %T", elector)
	}
}

func TestNewElectorAuto_K8sNoFactory(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")
	t.Setenv("DLOCK_ELECTOR_URL", "")
	_, err := dlock.NewElectorAuto()
	if err == nil {
		t.Fatal("expected error when k8s factory not registered")
	}
}
