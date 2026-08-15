// Package shelltool 提供带策略预检与输出截断的 shell 执行工具。
// Policy 是 UX 层 guardrail;真正的安全边界是 agent.Permission(默认 PermitAsk 需审批)。
package shelltool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
)

const (
	defaultShell     = "sh"
	defaultMaxOutput = 65536
	defaultTimeout   = 30

	toolName        = "shell"
	toolDescription = "在主机上执行 shell 命令并返回合并后的 stdout/stderr"
)

var toolParameters = json.RawMessage(`{
	"type": "object",
	"properties": {
		"command": {"type": "string", "description": "要执行的 shell 命令"}
	},
	"required": ["command"],
	"additionalProperties": false
}`)

// Policy 控制哪些命令可以执行。
// 评估顺序:允许列表优先 → 拒绝列表 → 默认放行。
type Policy struct {
	allows []policyPattern
	denies []policyPattern
}

type policyPattern struct {
	pattern string
	re      *regexp.Regexp
}

// PolicyConfig 配置命令策略。
type PolicyConfig struct {
	AllowList []string // 正则。匹配任一则直接允许。
	DenyList  []string // 正则。匹配任一则拒绝。
}

// NewPolicy 从配置创建 Policy。
func NewPolicy(cfg PolicyConfig) (*Policy, error) {
	p := &Policy{}
	for _, pat := range cfg.AllowList {
		re, err := regexp.Compile(pat)
		if err != nil {
			return nil, fmt.Errorf("shelltool: invalid allow pattern %q: %w", pat, err)
		}
		p.allows = append(p.allows, policyPattern{pattern: pat, re: re})
	}
	for _, pat := range cfg.DenyList {
		re, err := regexp.Compile(pat)
		if err != nil {
			return nil, fmt.Errorf("shelltool: invalid deny pattern %q: %w", pat, err)
		}
		p.denies = append(p.denies, policyPattern{pattern: pat, re: re})
	}
	return p, nil
}

// Evaluate 判断命令是否允许执行。返回 (allowed, reason)。
func (p *Policy) Evaluate(command string) (bool, string) {
	command = strings.TrimSpace(command)
	if command == "" {
		return false, "empty command"
	}
	if p == nil {
		return true, ""
	}
	for _, allow := range p.allows {
		if allow.re.MatchString(command) {
			return true, fmt.Sprintf("matched allow pattern %q", allow.pattern)
		}
	}
	for _, deny := range p.denies {
		if deny.re.MatchString(command) {
			return false, fmt.Sprintf("matched deny pattern %q", deny.pattern)
		}
	}
	return true, ""
}

// Config 是 shell tool 的配置。
type Config struct {
	Shell      string           // 默认 "sh"
	ShellArgs  []string         // 默认 ["-c"]
	WorkDir    string           // 工作目录
	MaxOutput  int              // 最大输出字节数(默认 65536)
	Timeout    int              // 超时秒数(默认 30)
	Policy     *Policy          // 命令策略(nil = 全部允许)
	Permission agent.Permission // 工具权限(默认 PermitAsk — 需审批)
	allowSet   bool             // 内部: 显式 PermitAllow(零值与未设置歧义)
}

// WithPermitAllow 返回显式 PermitAllow 的配置副本。
func WithPermitAllow(cfg Config) Config {
	cfg.Permission = agent.PermitAllow
	cfg.allowSet = true
	return cfg
}

func (c Config) effectivePermission() agent.Permission {
	if c.allowSet && c.Permission == agent.PermitAllow {
		return agent.PermitAllow
	}
	switch c.Permission {
	case agent.PermitDeny:
		return agent.PermitDeny
	case agent.PermitAsk:
		return agent.PermitAsk
	}
	return agent.PermitAsk
}

// Result 是命令执行结果。
type Result struct {
	ExitCode  int    `json:"exit_code"`
	Output    string `json:"output"`
	Truncated bool   `json:"truncated,omitempty"`
}

type shellInput struct {
	Command string `json:"command"`
}

// executor 执行 shell 命令;测试时可注入 mock。
type executor func(ctx context.Context, shell string, args []string, workDir string) ([]byte, int, error)

// New 创建 shell 工具。
func New(cfg Config) agent.Tool {
	return newShellTool(cfg, defaultExecutor)
}

func newShellTool(cfg Config, exec executor) agent.Tool {
	shell := cfg.Shell
	if shell == "" {
		shell = defaultShell
	}
	shellArgs := cfg.ShellArgs
	if len(shellArgs) == 0 {
		shellArgs = []string{"-c"}
	}
	maxOutput := cfg.MaxOutput
	if maxOutput <= 0 {
		maxOutput = defaultMaxOutput
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	call := func(ctx context.Context, args json.RawMessage) (string, error) {
		var in shellInput
		if len(args) > 0 && string(args) != "null" {
			if err := json.Unmarshal(args, &in); err != nil {
				return "", fmt.Errorf("shelltool: unmarshal args: %w", err)
			}
		}

		command := strings.TrimSpace(in.Command)
		if cfg.Policy != nil {
			if ok, reason := cfg.Policy.Evaluate(command); !ok {
				return "", fmt.Errorf("shelltool: command denied: %s", reason)
			}
		} else if command == "" {
			return "", fmt.Errorf("shelltool: command denied: empty command")
		}

		runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
		defer cancel()

		execArgs := append(append([]string{}, shellArgs...), command)
		output, exitCode, err := exec(runCtx, shell, execArgs, cfg.WorkDir)
		if err != nil {
			return "", fmt.Errorf("shelltool: execute: %w", err)
		}

		truncated, out := truncateHeadTail(output, maxOutput)
		result := Result{
			ExitCode:  exitCode,
			Output:    out,
			Truncated: truncated,
		}
		b, err := json.Marshal(result)
		if err != nil {
			return "", fmt.Errorf("shelltool: marshal result: %w", err)
		}
		return string(b), nil
	}

	return agent.Tool{
		Def: llm.ToolDef{
			Name:        toolName,
			Description: toolDescription,
			Parameters:  toolParameters,
		},
		Call:       call,
		Permission: cfg.effectivePermission(),
	}
}

func defaultExecutor(ctx context.Context, shell string, args []string, workDir string) ([]byte, int, error) {
	cmd := exec.CommandContext(ctx, shell, args...)
	if workDir != "" {
		cmd.Dir = workDir
	}
	output, err := cmd.CombinedOutput()
	if err == nil {
		return output, 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return output, exitErr.ExitCode(), nil
	}
	return output, -1, err
}

// truncateHeadTail 保留前一半与后一半字节,中间插入截断标记。
func truncateHeadTail(data []byte, maxBytes int) (truncated bool, out string) {
	if maxBytes <= 0 || len(data) <= maxBytes {
		return false, string(data)
	}
	half := maxBytes / 2
	head := data[:half]
	tail := data[len(data)-half:]
	removed := len(data) - len(head) - len(tail)
	marker := fmt.Sprintf("\n[... truncated %d bytes ...]\n", removed)
	return true, string(head) + marker + string(tail)
}
