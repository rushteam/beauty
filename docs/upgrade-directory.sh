#!/bin/bash

# 设置错误时退出
set -e

echo "开始目录结构升级..."

# 创建新目录
echo "创建新目录结构..."
mkdir -p pkg/utils/{addr,uuid,bloom} \
        pkg/service/{telemetry,logger,discover,core} \
        pkg/client/{grpc,http,nacos} \
        examples/{security,resilience,services,complete}

# 移动工具类文件
echo "移动工具类文件..."
mv pkg/addr/* pkg/utils/addr/ 2>/dev/null || true
mv pkg/uuid/* pkg/utils/uuid/ 2>/dev/null || true
mv pkg/libs/bloom/* pkg/utils/bloom/ 2>/dev/null || true

# 移动服务相关组件
echo "移动服务相关组件..."
mv pkg/tracing/* pkg/service/telemetry/ 2>/dev/null || true
mv pkg/logger/* pkg/service/logger/ 2>/dev/null || true
mv pkg/discover/* pkg/service/discover/ 2>/dev/null || true
mv pkg/core/* pkg/service/core/ 2>/dev/null || true

# 重构客户端包
echo "重构客户端包..."
mv pkg/client/grpcclient/* pkg/client/grpc/ 2>/dev/null || true
mv pkg/client/resty/* pkg/client/http/ 2>/dev/null || true

# 重组示例代码
echo "重组示例代码..."
mv example/auth-ratelimit examples/security/ 2>/dev/null || true
mv example/circuitbreaker examples/resilience/ 2>/dev/null || true
mv example/timeout examples/resilience/ 2>/dev/null || true
mv example/svc examples/services/ 2>/dev/null || true
mv example/example examples/complete/ 2>/dev/null || true

# 清理旧目录
echo "清理旧目录..."
rm -rf pkg/{addr,uuid,libs,tracing,logger,discover,core} pkg/client/{grpcclient,resty} example 2>/dev/null || true

# 更新导入路径
echo "更新导入路径..."
find . -type f -name "*.go" -exec sed -i '' \
    -e 's|"github.com/rushteam/beauty/pkg/addr"|"github.com/rushteam/beauty/pkg/utils/addr"|g' \
    -e 's|"github.com/rushteam/beauty/pkg/uuid"|"github.com/rushteam/beauty/pkg/utils/uuid"|g' \
    -e 's|"github.com/rushteam/beauty/pkg/libs/bloom"|"github.com/rushteam/beauty/pkg/utils/bloom"|g' \
    -e 's|"github.com/rushteam/beauty/pkg/tracing"|"github.com/rushteam/beauty/pkg/service/telemetry"|g' \
    -e 's|"github.com/rushteam/beauty/pkg/logger"|"github.com/rushteam/beauty/pkg/service/logger"|g' \
    -e 's|"github.com/rushteam/beauty/pkg/discover"|"github.com/rushteam/beauty/pkg/service/discover"|g' \
    -e 's|"github.com/rushteam/beauty/pkg/core"|"github.com/rushteam/beauty/pkg/service/core"|g' \
    -e 's|"github.com/rushteam/beauty/pkg/client/grpcclient"|"github.com/rushteam/beauty/pkg/client/grpcclient"|g' \
    -e 's|"github.com/rushteam/beauty/pkg/client/resty"|"github.com/rushteam/beauty/pkg/client/http"|g' \
    -e 's|"github.com/rushteam/beauty/example/example/api/v1"|"github.com/rushteam/beauty/examples/complete/example/api/v1"|g' \
    {} +

# 更新 README.md 中的导入路径
echo "更新文档中的导入路径..."
find . -type f -name "*.md" -exec sed -i '' \
    -e 's|"github.com/rushteam/beauty/pkg/discover/etcdv3"|"github.com/rushteam/beauty/pkg/service/discover/etcdv3"|g' \
    -e 's|"github.com/rushteam/beauty/pkg/tracing"|"github.com/rushteam/beauty/pkg/service/telemetry"|g' \
    {} +

echo "目录结构升级完成！"
echo "请运行 'go mod tidy' 更新依赖关系。"
