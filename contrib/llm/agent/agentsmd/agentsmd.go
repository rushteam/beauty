// Package agentsmd 提供 AGENTS.md 级联注入的 ContextProvider。
//
// 从工作目录沿父目录向上收集名为 AGENTS.md 的文件,按「仓库根 → cwd」
// (通用 → 具体)顺序拼进系统提示:越靠近工作目录的内容越靠后、优先级越高。
// 纯标准库,零外部依赖。
package agentsmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
)

const defaultFilename = "AGENTS.md"

// Provider 是 AGENTS.md 级联注入的 ContextProvider。
//
// 收集规则:
//  1. 从 Dir(默认进程 cwd)开始,逐级向父目录查找 Filename(默认 AGENTS.md);
//  2. 遇到含 .git 的目录时(StopAtGit=true,默认)将该层纳入后停止;
//  3. 若设置了 Root,到达 Root 后停止(含 Root 层);
//  4. 将找到的文件按「根 → cwd」顺序拼接,追加到 req.System。
type Provider struct {
	// Dir 是起始工作目录。空则用 os.Getwd()。
	Dir string
	// Root 是向上搜索的上限目录。空则依赖 StopAtGit 或文件系统根。
	Root string
	// Filename 要查找的文件名,默认 "AGENTS.md"。
	Filename string
	// StopAtGit 为 true(默认)时,遇到含 .git 的目录即停止向上。
	// 显式设为 false 可继续上溯到 Root / 文件系统根。
	StopAtGit *bool
	// Separator 多段 AGENTS.md 之间的分隔符,默认 "\n\n---\n\n"。
	Separator string
}

// New 用工作目录创建 Provider(其余字段默认)。
func New(dir string) *Provider {
	return &Provider{Dir: dir}
}

func (p *Provider) stopAtGit() bool {
	if p.StopAtGit == nil {
		return true
	}
	return *p.StopAtGit
}

func (p *Provider) filename() string {
	if p.Filename != "" {
		return p.Filename
	}
	return defaultFilename
}

func (p *Provider) separator() string {
	if p.Separator != "" {
		return p.Separator
	}
	return "\n\n---\n\n"
}

// Collect 从 Dir 向上收集 AGENTS.md 内容,返回「根 → cwd」顺序的正文切片。
func (p *Provider) Collect() ([]string, error) {
	dir := p.Dir
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		dir = wd
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	var absRoot string
	if p.Root != "" {
		absRoot, err = filepath.Abs(p.Root)
		if err != nil {
			return nil, err
		}
	}

	name := p.filename()
	// 自 cwd 向上收集(具体 → 通用),稍后反转为 通用 → 具体。
	var upward []string
	cur := absDir
	for {
		path := filepath.Join(cur, name)
		if data, err := os.ReadFile(path); err == nil {
			if text := strings.TrimSpace(string(data)); text != "" {
				upward = append(upward, text)
			}
		}

		if absRoot != "" && sameDir(cur, absRoot) {
			break
		}
		if p.stopAtGit() {
			if st, err := os.Stat(filepath.Join(cur, ".git")); err == nil && st != nil {
				break
			}
		}

		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}

	// 反转:根(通用) → cwd(具体)
	for i, j := 0, len(upward)-1; i < j; i, j = i+1, j-1 {
		upward[i], upward[j] = upward[j], upward[i]
	}
	return upward, nil
}

// SystemExtra 返回拼好的系统提示补充(无文件时为空)。
func (p *Provider) SystemExtra() (string, error) {
	parts, err := p.Collect()
	if err != nil {
		return "", err
	}
	if len(parts) == 0 {
		return "", nil
	}
	var b strings.Builder
	b.WriteString("以下是项目约定(AGENTS.md,由通用到具体):\n\n")
	b.WriteString(strings.Join(parts, p.separator()))
	return b.String(), nil
}

// Invoking 实现 agent.ContextProvider:把 AGENTS.md 级联内容追加到 req.System。
func (p *Provider) Invoking(_ context.Context, req *llm.Request) ([]llm.Message, []agent.Tool, error) {
	extra, err := p.SystemExtra()
	if err != nil {
		return nil, nil, err
	}
	if extra == "" {
		return nil, nil, nil
	}
	if req.System != "" {
		req.System += "\n\n"
	}
	req.System += extra
	return nil, nil, nil
}

// Invoked 实现 agent.ContextProvider(空操作)。
func (p *Provider) Invoked(context.Context, *agent.RunOutcome) error { return nil }

func sameDir(a, b string) bool {
	aa, err1 := filepath.Abs(a)
	bb, err2 := filepath.Abs(b)
	if err1 != nil || err2 != nil {
		return a == b
	}
	return aa == bb
}

// 编译期断言。
var _ agent.ContextProvider = (*Provider)(nil)
