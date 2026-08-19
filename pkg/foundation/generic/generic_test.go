package generic

import (
	"reflect"
	"testing"
)

func TestPtr(t *testing.T) {
	p := Ptr(42)
	if *p != 42 {
		t.Fatalf("want 42, got %d", *p)
	}
}

func TestNew(t *testing.T) {
	// map / slice 应已 make，可直接用
	m := New[map[string]int]()
	m["a"] = 1
	s := New[[]int]()
	s = append(s, 1)

	// 指针应非 nil 且指向零值
	p := New[*int]()
	if p == nil {
		t.Fatal("New[*int]() must not be nil")
	}
	if *p != 0 {
		t.Fatalf("want zero int, got %d", *p)
	}

	// 多级指针逐层分配
	pp := New[**int]()
	if pp == nil || *pp == nil {
		t.Fatal("New[**int]() must allocate both levels")
	}
}

func TestToMap(t *testing.T) {
	type user struct {
		id   int
		name string
	}
	users := []user{{1, "a"}, {2, "b"}}
	byID := ToMap(users, func(u user) (int, string) { return u.id, u.name })
	if byID[1] != "a" || byID[2] != "b" {
		t.Fatalf("unexpected map: %v", byID)
	}
}

func TestTypeName(t *testing.T) {
	type myStruct struct{}
	s := &myStruct{}
	if got := TypeName(reflect.ValueOf(s)); got != "myStruct" {
		t.Errorf("pointer should deref to myStruct, got %q", got)
	}
	if got := TypeName(reflect.ValueOf(TestTypeName)); got != "TestTypeName" {
		t.Errorf("named func should be TestTypeName, got %q", got)
	}
	if got := TypeName(reflect.ValueOf(func() {})); got != "" {
		t.Errorf("anonymous func should be empty, got %q", got)
	}
}
