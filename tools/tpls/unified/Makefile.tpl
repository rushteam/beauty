{{if .EnableGrpc}}# Makefile for {{.Name}} project

.PHONY: help generate tidy build run clean test

# 默认目标
.DEFAULT_GOAL := help

# 帮助信息
help: ## 显示帮助信息
	@echo "🚀 {{.Name}} 项目构建工具"
	@echo ""
	@echo "可用命令:"
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# 生成protobuf代码
generate: ## 生成protobuf代码
	@echo "🚀 开始生成protobuf代码..."
	@if ! command -v buf &> /dev/null; then \
		echo "❌ buf未安装，请先安装buf: https://docs.buf.build/installation"; \
		exit 1; \
	fi
	@echo "📦 更新buf依赖..."
	@buf dep update
	@echo "📦 生成Go代码..."
	@buf generate
	@echo "✅ protobuf代码生成完成！"
	@echo ""
	@echo "📋 生成的文件："
	@echo "  - api/v1/user.pb.go (protobuf消息)"
	@echo "  - api/v1/user_grpc.pb.go (gRPC服务)"
	@echo "  - api/v1/user.pb.gw.go (gRPC-Gateway)"

# 整理依赖
tidy: ## 整理Go模块依赖
	@echo "📦 整理Go模块依赖..."
	@go mod tidy
	@echo "✅ 依赖整理完成！"

# 构建项目
build: tidy ## 构建项目
	@echo "🔨 构建项目..."
	@go build -o {{.Name}} .
	@echo "✅ 构建完成！"

# 运行项目
run: build ## 构建并运行项目
	@echo "🚀 启动服务..."
	@./{{.Name}}

# 清理构建文件
clean: ## 清理构建文件
	@echo "🧹 清理构建文件..."
	@rm -f {{.Name}}
	@echo "✅ 清理完成！"

# 运行测试
test: ## 运行测试
	@echo "🧪 运行测试..."
	@go test -v ./...
	@echo "✅ 测试完成！"

# 开发模式（生成代码 + 构建 + 运行）
dev: generate tidy build run ## 开发模式：生成代码、构建并运行

# 完整构建流程
all: generate tidy build ## 完整构建流程：生成代码、整理依赖、构建
{{else}}# Makefile for {{.Name}} project

.PHONY: help tidy build run clean test

# 默认目标
.DEFAULT_GOAL := help

# 帮助信息
help: ## 显示帮助信息
	@echo "🚀 {{.Name}} 项目构建工具"
	@echo ""
	@echo "可用命令:"
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# 整理依赖
tidy: ## 整理Go模块依赖
	@echo "📦 整理Go模块依赖..."
	@go mod tidy
	@echo "✅ 依赖整理完成！"

# 构建项目
build: tidy ## 构建项目
	@echo "🔨 构建项目..."
	@go build -o {{.Name}} .
	@echo "✅ 构建完成！"

# 运行项目
run: build ## 构建并运行项目
	@echo "🚀 启动服务..."
	@./{{.Name}}

# 清理构建文件
clean: ## 清理构建文件
	@echo "🧹 清理构建文件..."
	@rm -f {{.Name}}
	@echo "✅ 清理完成！"

# 运行测试
test: ## 运行测试
	@echo "🧪 运行测试..."
	@go test -v ./...
	@echo "✅ 测试完成！"
{{end}}
