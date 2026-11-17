// Package mdrt 把受限 Markdown 方言编译成 jsvm 脚本并热切换。
//
// 一个 specs/*.md 是一个模块。路由、入参、鉴权和例子由解析器下降成 JavaScript;
// 只有「行为」小节交给 llm.Client 生成函数体。例子不过,旧的一代继续服务。
// 请求路径上不调用模型。
//
// 生产构建用 Build 把脚本冻到目录里,再用 jsvm.WithFS 直接跑,不必把本包链进二进制。
package mdrt

import _ "embed"

//go:embed prelude.js
var preludeSource string
