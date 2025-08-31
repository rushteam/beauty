# 目录结构升级指南

本文档描述了框架的目录结构升级过程，包括手动升级步骤和自动化脚本。

## 目录结构变更

### 主要变更

1. 工具类移动到 `pkg/utils` 目录：
   - `pkg/addr` → `pkg/utils/addr`
   - `pkg/uuid` → `pkg/utils/uuid`
   - `pkg/libs/bloom` → `pkg/utils/bloom`

2. 服务相关组件移动到 `pkg/service` 目录：
   - `pkg/tracing` → `pkg/service/telemetry`
   - `pkg/logger` → `pkg/service/logger`
   - `pkg/discover` → `pkg/service/discover`
   - `pkg/core` → `pkg/service/core`

3. 客户端包结构优化：
   - `pkg/client/grpcclient` → `pkg/client/grpc`
   - `pkg/client/resty` → `pkg/client/http`

4. 示例代码重组：
   - `example/auth-ratelimit` → `examples/security/auth-ratelimit`
   - `example/circuitbreaker` → `examples/resilience/circuitbreaker`
   - `example/timeout` → `examples/resilience/timeout`
   - `example/svc` → `examples/services/svc`
   - `example/example` → `examples/complete/example`

## 自动化升级脚本

将以下内容保存为 `upgrade-directory.sh`：

```bash
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
    -e 's|"github.com/rushteam/beauty/pkg/client/grpcclient"|"github.com/rushteam/beauty/pkg/client/grpc"|g' \
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
```

## 使用方法

1. 在项目根目录下保存升级脚本：

```bash
curl -o upgrade-directory.sh https://raw.githubusercontent.com/rushteam/beauty/main/docs/upgrade-directory.sh
```

2. 给脚本添加执行权限：

```bash
chmod +x upgrade-directory.sh
```

3. 运行升级脚本：

```bash
./upgrade-directory.sh
```

4. 更新依赖关系：

```bash
go mod tidy
```

## 注意事项

1. 在运行升级脚本前，建议：
   - 提交或暂存当前的代码更改
   - 创建新的 git 分支
   - 备份重要文件

2. 脚本执行后，需要：
   - 检查导入路径是否正确更新
   - 运行测试确保功能正常
   - 检查编译是否通过

3. 如果使用了自定义的目录结构或文件名，可能需要手动调整部分文件

## 手动升级步骤

如果你更倾向于手动升级，可以按照以下步骤进行：

1. 创建新的目录结构
2. 移动相关文件到新位置
3. 更新导入路径
4. 删除旧的空目录
5. 更新依赖关系

## 常见问题

1. Q: 升级后编译报错找不到包？
   A: 检查 `go.mod` 文件中的依赖版本，运行 `go mod tidy` 更新依赖关系。

2. Q: 某些文件没有被正确移动？
   A: 检查文件权限和路径是否正确，必要时手动移动文件。

3. Q: 导入路径更新不完整？
   A: 使用 IDE 的全局搜索功能，搜索旧的导入路径并手动更新。

## 回滚方法

如果需要回滚更改：

1. 如果使用 git：
   ```bash
   git reset --hard HEAD
   git clean -fd
   ```

2. 如果手动备份：
   - 恢复备份的文件
   - 删除新创建的目录
