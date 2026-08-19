module github.com/rushteam/beauty/contrib/wasmagent

go 1.26.0

toolchain go1.26.5

require (
	github.com/rushteam/beauty/contrib/llm v0.8.6
	github.com/rushteam/beauty/contrib/wasm v0.8.6
	github.com/tetratelabs/wazero v1.12.0
)

require golang.org/x/sys v0.44.0 // indirect

// 本地联调:胶水模块同时依赖 wasm(Tier1 运行时)与 llm/agent/skills(执行器注入口)。
// 发布前请去掉 replace,并把 require 指向已发布的 tag。

replace github.com/rushteam/beauty => ../../
