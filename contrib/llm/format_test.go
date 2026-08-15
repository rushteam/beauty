package llm_test

import (
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
)

type testOutput struct {
	Name    string   `json:"name"`
	Age     int      `json:"age"`
	Tags    []string `json:"tags,omitempty"`
	IsAdmin bool     `json:"is_admin"`
}

func TestNewTypedOutput(t *testing.T) {
	typed, err := llm.NewTypedOutput[testOutput]("test_output")
	if err != nil {
		t.Fatalf("NewTypedOutput error: %v", err)
	}

	rf := typed.Format()
	if rf == nil {
		t.Fatal("Format() returned nil")
	}
	if rf.Type != "json_schema" {
		t.Errorf("Format().Type = %q, want 'json_schema'", rf.Type)
	}
	if rf.JSONSchema == nil {
		t.Fatal("Format().JSONSchema is nil")
	}
	if rf.JSONSchema.Name != "test_output" {
		t.Errorf("JSONSchema.Name = %q, want 'test_output'", rf.JSONSchema.Name)
	}
	if !rf.JSONSchema.Strict {
		t.Error("JSONSchema.Strict should be true by default")
	}
}

func TestTypedOutput_Unmarshal(t *testing.T) {
	typed, err := llm.NewTypedOutput[testOutput]("test_output")
	if err != nil {
		t.Fatalf("NewTypedOutput error: %v", err)
	}

	resp := &llm.Response{Content: `{"name":"Alice","age":30,"is_admin":true}`}
	result, err := typed.Unmarshal(resp)
	if err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if result.Name != "Alice" {
		t.Errorf("Name = %q, want 'Alice'", result.Name)
	}
	if result.Age != 30 {
		t.Errorf("Age = %d, want 30", result.Age)
	}
	if !result.IsAdmin {
		t.Error("IsAdmin should be true")
	}
}

func TestTypedOutput_ApplyTo(t *testing.T) {
	typed, err := llm.NewTypedOutput[testOutput]("test_output")
	if err != nil {
		t.Fatalf("NewTypedOutput error: %v", err)
	}

	req := &llm.Request{Model: "test"}
	typed.ApplyTo(req)
	if req.ResponseFormat == nil {
		t.Fatal("ApplyTo should set ResponseFormat")
	}
	if req.ResponseFormat.Type != "json_schema" {
		t.Errorf("ResponseFormat.Type = %q, want 'json_schema'", req.ResponseFormat.Type)
	}
}

func TestTypedOutput_NonStrict(t *testing.T) {
	typed, err := llm.NewTypedOutput[testOutput]("test", llm.WithStrict(false))
	if err != nil {
		t.Fatalf("NewTypedOutput error: %v", err)
	}
	if typed.Format().JSONSchema.Strict {
		t.Error("Strict should be false when WithStrict(false)")
	}
}
