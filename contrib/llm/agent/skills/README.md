# llm/agent/skills —— Agent Skills(SKILL.md)

在 [`llm/agent`](..) 之上实现 **Agent Skills**(与 Claude Code 的 `SKILL.md` 同规范):
一个技能 = 一个目录(`SKILL.md` + 可选 `scripts/`、`references/`)。加载后以**渐进式披露**
接入 `agent.Runner`——系统提示里只放技能名录,模型命中任务时才按需拉全文/读引用/跑脚本。
纯标准库,零外部依赖。

## 目录结构

```
skills/
  pdf-tools/
    SKILL.md            # frontmatter(name/description/license/allowed-tools) + 正文=instructions
    references/         # 可选:文档,按需读
      cheatsheet.md
    scripts/            # 可选:脚本,读或(开启后)执行
      extract.py
```

`SKILL.md`:

```markdown
---
name: pdf-tools
description: 处理 PDF:抽取文本、合并、拆分
license: MIT
allowed-tools: [Read, Bash]
---
# PDF Tools
当用户要处理 PDF 时……(完整指令写在正文)
```

## 用法

```go
import (
    "github.com/rushteam/beauty/contrib/llm"
    "github.com/rushteam/beauty/contrib/llm/agent"
    "github.com/rushteam/beauty/contrib/llm/agent/skills"
    "github.com/rushteam/beauty/contrib/llm/openai"
)

sk, _ := skills.Load(skills.LocalSkills{Dir: "./skills"})

r := &agent.Runner{Client: openai.New(key), Tools: sk.Tools()}
resp, _ := r.Run(ctx, llm.Request{
    Model:    "gpt-4o",
    System:   sk.SystemPrompt(),   // 技能名录(渐进式披露,不含正文)
    Messages: []llm.Message{{Role: llm.User, Content: "帮我把这个 PDF 拆成单页"}},
})
```

- **`SystemPrompt()`**:每个技能只暴露 名字/描述/文件清单 + 用法说明,不含正文。
- **`Tools()`**:三个元工具,和其它工具一样交给 `Runner`:
  - `get_skill_instructions(skill_name)` — 拉全文
  - `get_skill_reference(skill_name, reference_path)` — 读引用文档
  - `get_skill_script(skill_name, script_path, execute)` — 读或执行脚本
- 与 [`mcpagent`](../../../mcpagent)、普通 `agent.Tool` 可混用(同一个 `Runner.Tools` 里)。

## 脚本执行(默认关闭)

执行任意本地脚本是安全敏感操作(policy),默认**只读**;`execute=true` 会被拒。显式开启:

```go
sk.EnableExec(30 * time.Second) // 允许执行,单次超时 30s
```

- 文件访问带**路径穿越防护**(只能读登记在 `scripts/`、`references/` 里的文件,`../` 越界被拒)。
- 执行用 `exec.CommandContext` + 超时;脚本需自带 shebang 且可执行,`cwd` 为技能目录。

## 边界

技能内容、给模型哪些技能、要不要允许执行都是 policy。本包只做"加载 + 校验 + 名录 + 三个工具",
不内置任何技能。校验遵循规范:`name` 小写字母/数字/连字符且 ≤64、`description` 必填 ≤1024。
