package diff

import (
	"encoding/json"
	"reflect"
	"testing"
)

func mustJSON(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("unmarshal %q: %v", s, err)
	}
	return v
}

func TestJSONDiffApplyRoundTrip(t *testing.T) {
	cases := []struct{ a, b string }{
		{`{"a":1,"b":2}`, `{"a":1,"b":3}`},                                   // replace
		{`{"a":1}`, `{"a":1,"c":9}`},                                         // add
		{`{"a":1,"b":2}`, `{"a":1}`},                                         // remove
		{`{"user":{"name":"x","age":10}}`, `{"user":{"name":"y","age":10}}`}, // nested replace
		{`{"tags":["a","b"]}`, `{"tags":["a","c"]}`},                         // array same-len recurse
		{`{"tags":["a"]}`, `{"tags":["a","b"]}`},                             // array len change -> replace whole
		{`{}`, `{"x":{"y":[1,2,3]}}`},                                        // add complex
		{`1`, `2`},                                                           // root scalar replace
		{`{"a":1}`, `{"a":1}`},                                               // no change
	}
	for i, c := range cases {
		a, b := mustJSON(t, c.a), mustJSON(t, c.b)
		patch := JSONDiff(a, b)
		got, err := ApplyJSON(a, patch)
		if err != nil {
			t.Fatalf("case %d apply: %v (patch=%+v)", i, err, patch)
		}
		if !reflect.DeepEqual(got, b) {
			t.Fatalf("case %d: got %v want %v (patch=%+v)", i, got, b, patch)
		}
	}
}

func TestApplyJSONDoesNotMutateInput(t *testing.T) {
	a := mustJSON(t, `{"a":1,"b":{"c":2}}`)
	orig := mustJSON(t, `{"a":1,"b":{"c":2}}`)
	_, err := ApplyJSON(a, []JSONOp{{Op: "replace", Path: "/b/c", Value: float64(9)}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, orig) {
		t.Fatalf("input mutated: %v", a)
	}
}

func TestApplyJSONMoveCopyTest(t *testing.T) {
	doc := mustJSON(t, `{"a":1,"b":2,"arr":[10,20,30]}`)

	got, err := ApplyJSON(doc, []JSONOp{{Op: "move", From: "/a", Path: "/c"}})
	if err != nil {
		t.Fatal(err)
	}
	want := mustJSON(t, `{"b":2,"c":1,"arr":[10,20,30]}`)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("move: got %v want %v", got, want)
	}

	got, err = ApplyJSON(doc, []JSONOp{{Op: "copy", From: "/b", Path: "/arr/-"}})
	if err != nil {
		t.Fatal(err)
	}
	want = mustJSON(t, `{"a":1,"b":2,"arr":[10,20,30,2]}`)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("copy append: got %v want %v", got, want)
	}

	if _, err := ApplyJSON(doc, []JSONOp{{Op: "test", Path: "/a", Value: float64(1)}}); err != nil {
		t.Fatalf("test pass should succeed: %v", err)
	}
	if _, err := ApplyJSON(doc, []JSONOp{{Op: "test", Path: "/a", Value: float64(99)}}); err == nil {
		t.Fatal("test mismatch should fail")
	}
}

func TestApplyJSONArrayInsert(t *testing.T) {
	doc := mustJSON(t, `[1,2,3]`)
	got, err := ApplyJSON(doc, []JSONOp{{Op: "add", Path: "/1", Value: float64(9)}})
	if err != nil {
		t.Fatal(err)
	}
	want := mustJSON(t, `[1,9,2,3]`)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("array insert: got %v want %v", got, want)
	}
}

func TestJSONArrayAlignDiff(t *testing.T) {
	a := mustJSON(t, `{"tags":["x","y","z"]}`)
	b := mustJSON(t, `{"tags":["x","z"]}`) // 删除中间的 y
	patch := JSONDiff(a, b)
	// 应是一次 remove,而非整体 replace。
	if len(patch) != 1 || patch[0].Op != "remove" || patch[0].Path != "/tags/1" {
		t.Fatalf("expected single remove /tags/1, got %+v", patch)
	}
	got, err := ApplyJSON(a, patch)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, b) {
		t.Fatalf("got %v want %v", got, b)
	}
}

func TestJSONArrayInsertDiff(t *testing.T) {
	a := mustJSON(t, `[1,2,3]`)
	b := mustJSON(t, `[1,9,2,3,4]`)
	patch := JSONDiff(a, b)
	got, err := ApplyJSON(a, patch)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, b) {
		t.Fatalf("got %v want %v (patch=%+v)", got, b, patch)
	}
	for _, op := range patch { // 全部是 add,不应有整体 replace
		if op.Op == "replace" && op.Path == "" {
			t.Fatalf("unexpected whole replace: %+v", patch)
		}
	}
}

func TestEscapedPointer(t *testing.T) {
	doc := mustJSON(t, `{"a/b":1,"m~n":2}`)
	v, err := jsonGet(doc, "/a~1b")
	if err != nil || v != float64(1) {
		t.Fatalf("escaped / : %v %v", v, err)
	}
	v, err = jsonGet(doc, "/m~0n")
	if err != nil || v != float64(2) {
		t.Fatalf("escaped ~ : %v %v", v, err)
	}
}
