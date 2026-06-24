.PHONY: help tidy build run test clean
.DEFAULT_GOAL := help

help: ## 显示帮助信息
	@echo "🚀 {{.Name}} 项目构建工具"
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

tidy: ## 整理 Go 模块依赖
	@go mod tidy

build: tidy ## 构建可执行文件
	@go build -o {{.Name}} .

run: ## 运行服务
	@go run . --config config/dev/app.yaml

test: ## 运行测试
	@go test ./...

clean: ## 清理构建产物
	@rm -f {{.Name}}
