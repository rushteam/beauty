# llm/agent/skills —— Agent Skills(SKILL.md)

在 [`llm/agent`](..) 之上实现 **Agent Skills**(与 Claude Code 的 `SKILL.md` 同规范):
一个技能 = 一个目录(`SKILL.md` + 可选 `scripts/`、`references/`)。加载后以**渐进式披露**
接入 `agent.Runner`——系统提示里只放 `name` / `description` / `location`,模型命中任务时才按需拉全文/读引用/跑脚本。
纯标准库,零外部依赖。

## 目录结构

```
skills/
  pdf-tools/
    SKILL.md            # frontmatter + 正文=instructions
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
# 可选:不进模型目录,仅斜杠/显式调用
# disable-model-invocation: true
---
# PDF Tools
当用户要处理 PDF 时……(完整指令写在正文)
```

## 用法

```go
sk, _ := skills.Load(skills.LocalSkills{Dir: "./skills"})

// 方式 A:手动拼 system + tools
r := &agent.Runner{Client: client, Tools: sk.Tools()}
resp, _ := r.Run(ctx, llm.Request{
    Model:  "gpt-4o",
    System: sk.SystemPrompt(),
    Messages: []llm.Message{{Role: llm.User, Content: "帮我拆 PDF"}},
})

// 方式 B:作为 ContextProvider(推荐)
r := &agent.Runner{
    Client:       client,
    ContextProvs: []agent.ContextProvider{sk.AsContextProvider()},
}
```

### 渐进式披露

| 层 | 内容 |
|----|------|
| `SystemPrompt()` | 仅 `name` / `description` / `location`,标签 `<available_skills>` |
| `get_skill_instructions` | 按需拉正文 + scripts/references 清单 |
| `get_skill_reference` / `get_skill_script` | 再按需读文件或执行 |

`disable-model-invocation: true` 的技能**不进目录**,但 `Expand(name)` / `get_skill_instructions` 仍可用(斜杠命令场景)。

## 脚本执行(默认关闭)

```go
sk.EnableExec(30 * time.Second)
```

文件访问带路径穿越防护;执行需 `EnableExec`。

## 边界

技能内容、给模型哪些技能、要不要允许执行都是 policy。本包只做「加载 + 校验 + 名录 + 三个工具」。
