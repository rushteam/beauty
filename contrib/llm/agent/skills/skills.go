package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
)

// Skills 是加载后的技能集合,负责生成 system prompt 目录快照与三个元工具,供 agent.Runner 使用。
// 脚本执行默认关闭(只读),用 EnableExec 显式开启。
type Skills struct {
	byName      map[string]Skill
	order       []string
	allowExec   bool
	execTimeout time.Duration
	executor    ScriptExecutor
}

// ScriptExecutor 是脚本执行器:在(可能沙箱化的)环境里运行技能脚本。
// path 是脚本绝对路径(已通过路径穿越校验、已确认登记在该技能的 scripts/ 清单内);
// cwd 是技能目录;args 是模型给的参数。返回喂回模型的输出文本。
//
// 默认执行器是本地进程(exec.Command);用 WithScriptExecutor 可替换成沙箱执行
// (如 contrib/wasmagent 的 wasm 执行器)——把"信任本地脚本"变成"能力受限、无法逃逸"。
type ScriptExecutor func(ctx context.Context, path, cwd string, args []string) (string, error)

// Load 从若干 loader 加载全部技能(按名去重,后者覆盖先者)。
func Load(loaders ...Loader) (*Skills, error) {
	s := &Skills{byName: map[string]Skill{}, execTimeout: 30 * time.Second}
	for _, l := range loaders {
		list, err := l.Load()
		if err != nil {
			return nil, err
		}
		for _, sk := range list {
			if _, dup := s.byName[sk.Name]; !dup {
				s.order = append(s.order, sk.Name)
			}
			s.byName[sk.Name] = sk
		}
	}
	return s, nil
}

// EnableExec 允许 get_skill_script 以 execute=true 运行脚本,并设置单次执行超时(<=0 用 30s)。
// 默认关闭:执行任意本地脚本是安全敏感操作(policy),需调用方显式开启。返回自身便于链式调用。
func (s *Skills) EnableExec(timeout time.Duration) *Skills {
	s.allowExec = true
	if timeout > 0 {
		s.execTimeout = timeout
	}
	return s
}

// WithScriptExecutor 用自定义执行器替换默认的本地进程执行(exec.Command)——
// 典型用法是接 contrib/wasmagent 的 wasm 沙箱执行器。仍需 EnableExec 开启执行。
// 返回自身便于链式调用。
func (s *Skills) WithScriptExecutor(exec ScriptExecutor) *Skills {
	s.executor = exec
	return s
}

// Get 按名取技能。
func (s *Skills) Get(name string) (Skill, bool) { sk, ok := s.byName[name]; return sk, ok }

// All 返回全部技能(按加载顺序)。
func (s *Skills) All() []Skill {
	out := make([]Skill, 0, len(s.order))
	for _, n := range s.order {
		out = append(out, s.byName[n])
	}
	return out
}

// SystemPrompt 生成技能目录快照:只含 name / description / location(路径),不含正文(渐进式披露)。
// DisableModelInvocation 的技能会被排除(仅保留斜杠/显式调用)。
// 把它并进 llm.Request.System 或作为一条 system 消息。无可见技能时返回空串。
func (s *Skills) SystemPrompt() string {
	visible := s.modelVisible()
	if len(visible) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<available_skills>\n")
	b.WriteString("你可以使用下列技能(领域知识包)。目录只含名称与描述;完整说明需按需加载:\n")
	b.WriteString("1. get_skill_instructions(skill_name):命中任务时先取该技能的完整说明;\n")
	b.WriteString("2. get_skill_reference(skill_name, reference_path):按需读引用文档;\n")
	b.WriteString("3. get_skill_script(skill_name, script_path, execute):读或(允许时)执行脚本。\n")
	b.WriteString("仅在需要时才加载详情。可用技能:\n")
	for _, sk := range visible {
		b.WriteString("<skill>\n")
		fmt.Fprintf(&b, "  <name>%s</name>\n", sk.Name)
		fmt.Fprintf(&b, "  <description>%s</description>\n", sk.Description)
		if sk.SourcePath != "" {
			fmt.Fprintf(&b, "  <location>%s</location>\n", sk.SourcePath)
		}
		b.WriteString("</skill>\n")
	}
	b.WriteString("</available_skills>")
	return b.String()
}

// modelVisible 返回可被模型自动发现的技能(排除 DisableModelInvocation)。
func (s *Skills) modelVisible() []Skill {
	out := make([]Skill, 0, len(s.order))
	for _, n := range s.order {
		sk := s.byName[n]
		if sk.DisableModelInvocation {
			continue
		}
		out = append(out, sk)
	}
	return out
}

// Expand 按名返回技能正文(供斜杠命令 /skill-name 显式展开)。
// 含 DisableModelInvocation 的技能也可展开。不存在返回 false。
func (s *Skills) Expand(name string) (Skill, bool) {
	return s.Get(name)
}

// AsContextProvider 把技能集合适配为 agent.ContextProvider:
// 每次 Invoking 将 SystemPrompt 追加到 req.System,并注入三个元工具。
func (s *Skills) AsContextProvider() agent.ContextProvider {
	return &skillsContextProvider{s: s}
}

type skillsContextProvider struct{ s *Skills }

func (p *skillsContextProvider) Invoking(_ context.Context, req *llm.Request) ([]llm.Message, []agent.Tool, error) {
	if sp := p.s.SystemPrompt(); sp != "" {
		if req.System != "" {
			req.System += "\n\n"
		}
		req.System += sp
	}
	return nil, p.s.Tools(), nil
}

func (p *skillsContextProvider) Invoked(context.Context, *agent.RunOutcome) error { return nil }

// Tools 返回三个访问技能的 agent.Tool,直接放进 agent.Runner.Tools。
func (s *Skills) Tools() []agent.Tool {
	return []agent.Tool{
		{
			Def: llm.ToolDef{
				Name:        "get_skill_instructions",
				Description: "加载某技能的完整说明。命中一个技能对应的任务时先调它。",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"skill_name":{"type":"string"}},"required":["skill_name"]}`),
			},
			Call: s.getInstructions,
		},
		{
			Def: llm.ToolDef{
				Name:        "get_skill_reference",
				Description: "读取某技能 references/ 下的一个文档。",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"skill_name":{"type":"string"},"reference_path":{"type":"string"}},"required":["skill_name","reference_path"]}`),
			},
			Call: s.getReference,
		},
		{
			Def: llm.ToolDef{
				Name:        "get_skill_script",
				Description: "读取或执行某技能 scripts/ 下的脚本。execute=true 运行并返回输出(需服务端开启),否则(默认)返回脚本内容。",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"skill_name":{"type":"string"},"script_path":{"type":"string"},"execute":{"type":"boolean"},"args":{"type":"array","items":{"type":"string"}}},"required":["skill_name","script_path"]}`),
			},
			Call: s.getScript,
		},
	}
}

// —— 工具实现:返回值是喂回模型的 JSON 字符串;“找不到”类问题以 JSON 里的 error 字段返回
//    (附可用清单),让模型自行纠正,而非中断循环。真正的 IO/执行错误也同样以 JSON 返回。

func jsonResult(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (s *Skills) getInstructions(_ context.Context, args json.RawMessage) (string, error) {
	var in struct {
		SkillName string `json:"skill_name"`
	}
	_ = json.Unmarshal(args, &in)
	sk, ok := s.byName[in.SkillName]
	if !ok {
		return jsonResult(map[string]any{"error": fmt.Sprintf("技能 %q 不存在", in.SkillName), "available_skills": s.order})
	}
	return jsonResult(map[string]any{
		"skill_name":           sk.Name,
		"description":          sk.Description,
		"instructions":         sk.Instructions,
		"available_scripts":    sk.Scripts,
		"available_references": sk.References,
	})
}

func (s *Skills) getReference(_ context.Context, args json.RawMessage) (string, error) {
	var in struct {
		SkillName     string `json:"skill_name"`
		ReferencePath string `json:"reference_path"`
	}
	_ = json.Unmarshal(args, &in)
	sk, ok := s.byName[in.SkillName]
	if !ok {
		return jsonResult(map[string]any{"error": fmt.Sprintf("技能 %q 不存在", in.SkillName), "available_skills": s.order})
	}
	if !contains(sk.References, in.ReferencePath) {
		return jsonResult(map[string]any{"error": fmt.Sprintf("引用 %q 不在技能 %q 中", in.ReferencePath, sk.Name), "available_references": sk.References})
	}
	path, err := safeJoin(filepath.Join(sk.SourcePath, "references"), in.ReferencePath)
	if err != nil {
		return jsonResult(map[string]any{"error": "非法引用路径"})
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return jsonResult(map[string]any{"error": fmt.Sprintf("读引用失败: %v", err)})
	}
	return jsonResult(map[string]any{"skill_name": sk.Name, "reference_path": in.ReferencePath, "content": string(data)})
}

func (s *Skills) getScript(ctx context.Context, args json.RawMessage) (string, error) {
	var in struct {
		SkillName  string   `json:"skill_name"`
		ScriptPath string   `json:"script_path"`
		Execute    bool     `json:"execute"`
		Args       []string `json:"args"`
	}
	_ = json.Unmarshal(args, &in)
	sk, ok := s.byName[in.SkillName]
	if !ok {
		return jsonResult(map[string]any{"error": fmt.Sprintf("技能 %q 不存在", in.SkillName), "available_skills": s.order})
	}
	if !contains(sk.Scripts, in.ScriptPath) {
		return jsonResult(map[string]any{"error": fmt.Sprintf("脚本 %q 不在技能 %q 中", in.ScriptPath, sk.Name), "available_scripts": sk.Scripts})
	}
	path, err := safeJoin(filepath.Join(sk.SourcePath, "scripts"), in.ScriptPath)
	if err != nil {
		return jsonResult(map[string]any{"error": "非法脚本路径"})
	}
	if !in.Execute {
		data, err := os.ReadFile(path)
		if err != nil {
			return jsonResult(map[string]any{"error": fmt.Sprintf("读脚本失败: %v", err)})
		}
		return jsonResult(map[string]any{"skill_name": sk.Name, "script_path": in.ScriptPath, "content": string(data)})
	}
	if !s.allowExec {
		return jsonResult(map[string]any{"error": "脚本执行未开启(服务端需 EnableExec);可改用 execute=false 读取内容"})
	}
	cctx, cancel := context.WithTimeout(ctx, s.execTimeout)
	defer cancel()
	var out string
	var runErr error
	if s.executor != nil {
		out, runErr = s.executor(cctx, path, sk.SourcePath, in.Args)
	} else {
		cmd := exec.CommandContext(cctx, path, in.Args...)
		cmd.Dir = sk.SourcePath
		o, e := cmd.CombinedOutput()
		out, runErr = string(o), e
	}
	res := map[string]any{"skill_name": sk.Name, "script_path": in.ScriptPath, "output": out}
	if cctx.Err() == context.DeadlineExceeded {
		res["error"] = fmt.Sprintf("执行超时(%s)", s.execTimeout)
	} else if runErr != nil {
		res["error"] = runErr.Error()
	}
	return jsonResult(res)
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// safeJoin 把相对路径拼到 base 下,并确保结果不逃逸出 base(防路径穿越)。
func safeJoin(base, rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("skills: 不接受绝对路径")
	}
	joined := filepath.Join(base, rel)
	absBase, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	absJoined, err := filepath.Abs(joined)
	if err != nil {
		return "", err
	}
	if absJoined != absBase && !strings.HasPrefix(absJoined, absBase+string(filepath.Separator)) {
		return "", fmt.Errorf("skills: 路径逃逸出目录")
	}
	return joined, nil
}
