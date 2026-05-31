# 日志

Beauty 的日志模块基于标准库 `log/slog`，输出到 stderr，支持**运行时动态调整日志级别**，无需重启服务。

## 基本用法

```go
import "github.com/rushteam/beauty/pkg/service/logger"

logger.Debug("debug message", "key", "value")
logger.Info("server started", "addr", ":8080")
logger.Warn("slow query", "duration", "2s")
logger.Error("connect failed", "err", err)
```

所有日志条目带有 `beauty` group 前缀，方便在日志系统中过滤。

## 动态日志级别

### 代码方式

```go
import (
    "log/slog"
    "github.com/rushteam/beauty/pkg/service/logger"
)

// 按级别常量设置
logger.SetLevel(slog.LevelDebug)

// 按名称设置（不区分大小写）
logger.SetLevelByName("debug")  // debug / info / warn / error
logger.SetLevelByName("INFO")

// 读取当前级别
current := logger.GetLevel()
```

### HTTP 接口方式

将 `logger.LevelHandler()` 挂载到任意路由：

```go
mux.Handle("/debug/loglevel", logger.LevelHandler())
```

接口说明：

```
# 查询当前级别
GET /debug/loglevel
→ {"level":"info"}

# 修改级别（不重启生效）
PUT /debug/loglevel
Body: {"level":"debug"}
→ {"level":"debug"}
```

错误响应返回 HTTP 400：

```json
{"error": "unknown log level \"trace\": must be debug, info, warn or error"}
```

## 推荐集成方式

在 webserver 中挂载 loglevel 接口，生产环境可配合认证中间件保护：

```go
r := chi.NewRouter()
r.Handle("/debug/loglevel", logger.LevelHandler())

app := beauty.New(
    beauty.WithWebServer(":8080", r),
)
```
