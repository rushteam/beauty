// Package api 提供 HTTP/API 层的安全、错误处理、元数据与可观测机制。
//
// 选包标准:
//   - 与 HTTP 请求/响应生命周期相关(认证、错误码、审计、元数据传播等)
//   - 不涉及持久连接/推送(那属于 transport/)
//   - 可依赖 foundation/、resilience/(如 ratelimit)
//
// 已有子包:
//
//	handler      — 类型安全的 HTTP handler 注册(自动 JSON 编解码)
//	errors       — 结构化错误码(gRPC/HTTP 双模)
//	dberr        — 数据库错误到 API 错误的映射
//	audit        — 审计日志
//	authz        — RBAC/ABAC 授权接口(contrib/ 提供 casbin/openfga 实现)
//	token        — 双 Token(access+refresh)+ 黑名单注销
//	metadata     — 请求元数据提取与传播(含 propagation 子包)
//	featureflag  — 特性开关
//	afterwork    — 请求结束后的异步清理任务
//	callbacks    — 回调构建器(Builder 模式)
package api
