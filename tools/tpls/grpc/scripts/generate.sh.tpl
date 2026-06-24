#!/bin/bash

# 生成protobuf代码脚本

set -e

echo "🚀 开始生成protobuf代码..."

# 检查buf是否安装
if ! command -v buf &> /dev/null; then
    echo "❌ buf未安装，请先安装buf: https://docs.buf.build/installation"
    exit 1
fi

# 更新依赖
echo "📦 更新buf依赖..."
buf dep update

# 生成代码
echo "📦 生成Go代码..."
buf generate

echo "✅ protobuf代码生成完成！"
echo ""
echo "📋 生成的文件："
echo "  - api/v1/user.pb.go (protobuf消息)"
echo "  - api/v1/user_grpc.pb.go (gRPC服务)"
echo ""
echo "💡 现在可以运行: go mod tidy && go run main.go"
