package skills_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rushteam/beauty/contrib/llm/agent/skills"
)

// writeSkill 在 root 下造一个技能目录(SKILL.md + 可选 references/scripts 文件)。
func writeSkill(t *testing.T, root, name, front, body string, refs, scripts map[string]string) {
	t.Helper()
	dir := filepath.Join(root, name)
	mustMkdir(t, dir)
	mustWrite(t, filepath.Join(dir, "SKILL.md"), "---\n"+front+"\n---\n"+body)
	if len(refs) > 0 {
		mustMkdir(t, filepath.Join(dir, "references"))
		for fn, c := range refs {
			mustWrite(t, filepath.Join(dir, "references", fn), c)
		}
	}
	if len(scripts) > 0 {
		mustMkdir(t, filepath.Join(dir, "scripts"))
		for fn, c := range scripts {
			p := filepath.Join(dir, "scripts", fn)
			mustWrite(t, p, c)
			_ = os.Chmod(p, 0o755)
		}
	}
}

func mustMkdir(t *testing.T, d string) {
	t.Helper()
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
}
func mustWrite(t *testing.T, p, c string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
		t.Fatal(err)
	}
}

func decode(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("工具返回非 JSON: %v (%s)", err, s)
	}
	return m
}

// toolByName 从 Skills.Tools() 找指定工具。
func toolByName(t *testing.T, sk *skills.Skills, name string) func(context.Context, json.RawMessage) (string, error) {
	t.Helper()
	for _, tl := range sk.Tools() {
		if tl.Def.Name == name {
			return tl.Call
		}
	}
	t.Fatalf("未找到工具 %s", name)
	return nil
}

func loadOne(t *testing.T) *skills.Skills {
	t.Helper()
	root := t.TempDir()
	writeSkill(t, root, "greeter",
		"name: greeter\ndescription: 打招呼技能\nlicense: MIT\nallowed-tools: [Read, Bash]",
		"# Greeter\n按用户语言问候。",
		map[string]string{"notes.md": "参考:中文用你好"},
		map[string]string{"hello.sh": "#!/bin/sh\necho hi-from-script"},
	)
	sk, err := skills.Load(skills.LocalSkills{Dir: root})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return sk
}

func TestLoad_ParsesSkill(t *testing.T) {
	sk := loadOne(t)
	got, ok := sk.Get("greeter")
	if !ok {
		t.Fatal("未加载 greeter")
	}
	if got.Description != "打招呼技能" || got.License != "MIT" {
		t.Fatalf("skill = %+v", got)
	}
	if !strings.Contains(got.Instructions, "按用户语言问候") {
		t.Fatalf("正文 = %q", got.Instructions)
	}
	if len(got.AllowedTools) != 2 || got.AllowedTools[0] != "Read" {
		t.Fatalf("allowed-tools = %v", got.AllowedTools)
	}
	if len(got.Scripts) != 1 || got.Scripts[0] != "hello.sh" || len(got.References) != 1 {
		t.Fatalf("scripts=%v references=%v", got.Scripts, got.References)
	}
}

func TestSystemPrompt_CatalogOnly(t *testing.T) {
	sk := loadOne(t)
	p := sk.SystemPrompt()
	if !strings.Contains(p, "greeter") || !strings.Contains(p, "打招呼技能") {
		t.Fatalf("目录应含名字+描述: %s", p)
	}
	if !strings.Contains(p, "get_skill_instructions") {
		t.Fatal("应说明用元工具访问")
	}
	// 渐进式披露:目录里不应泄露正文。
	if strings.Contains(p, "按用户语言问候") {
		t.Fatal("system prompt 不应包含技能正文")
	}
}

func TestTool_GetInstructions(t *testing.T) {
	sk := loadOne(t)
	call := toolByName(t, sk, "get_skill_instructions")

	out, _ := call(context.Background(), json.RawMessage(`{"skill_name":"greeter"}`))
	m := decode(t, out)
	if !strings.Contains(m["instructions"].(string), "按用户语言问候") {
		t.Fatalf("应返回正文: %v", m)
	}

	// 不存在的技能 → error + 可用清单(不报 Go error,让模型自纠)。
	out, err := call(context.Background(), json.RawMessage(`{"skill_name":"nope"}`))
	if err != nil {
		t.Fatalf("找不到不应是 Go error: %v", err)
	}
	if decode(t, out)["error"] == nil {
		t.Fatal("应返回 error 字段")
	}
}

func TestTool_GetReference_And_Traversal(t *testing.T) {
	sk := loadOne(t)
	call := toolByName(t, sk, "get_skill_reference")

	out, _ := call(context.Background(), json.RawMessage(`{"skill_name":"greeter","reference_path":"notes.md"}`))
	if !strings.Contains(decode(t, out)["content"].(string), "你好") {
		t.Fatalf("应返回引用内容: %s", out)
	}

	// 路径穿越:不在清单内 → 拒绝。
	out, _ = call(context.Background(), json.RawMessage(`{"skill_name":"greeter","reference_path":"../SKILL.md"}`))
	if decode(t, out)["error"] == nil {
		t.Fatal("越界引用应被拒绝")
	}
}

func TestTool_GetScript_ReadAndExecGate(t *testing.T) {
	sk := loadOne(t)
	call := toolByName(t, sk, "get_skill_script")

	// 读取(默认)。
	out, _ := call(context.Background(), json.RawMessage(`{"skill_name":"greeter","script_path":"hello.sh"}`))
	if !strings.Contains(decode(t, out)["content"].(string), "hi-from-script") {
		t.Fatalf("应返回脚本内容: %s", out)
	}

	// 执行但未开启 → 被拦。
	out, _ = call(context.Background(), json.RawMessage(`{"skill_name":"greeter","script_path":"hello.sh","execute":true}`))
	if decode(t, out)["error"] == nil {
		t.Fatal("未 EnableExec 时执行应被拦")
	}

	// 开启后执行 → 拿到输出。
	sk.EnableExec(5 * time.Second)
	out, _ = call(context.Background(), json.RawMessage(`{"skill_name":"greeter","script_path":"hello.sh","execute":true}`))
	m := decode(t, out)
	if m["error"] != nil || !strings.Contains(m["output"].(string), "hi-from-script") {
		t.Fatalf("执行结果 = %v", m)
	}
}

func TestLoad_ValidationErrors(t *testing.T) {
	// 缺 description。
	root := t.TempDir()
	writeSkill(t, root, "bad", "name: bad", "body", nil, nil)
	if _, err := skills.Load(skills.LocalSkills{Dir: root}); err == nil {
		t.Fatal("缺 description 应报错")
	}

	// name 非法(大写)。
	root2 := t.TempDir()
	writeSkill(t, root2, "Bad", "name: Bad\ndescription: x", "body", nil, nil)
	if _, err := skills.Load(skills.LocalSkills{Dir: root2}); err == nil {
		t.Fatal("非法 name 应报错")
	}
}
