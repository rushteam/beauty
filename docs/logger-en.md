# Logging

Beauty's logging module is built on the standard library `log/slog`, writes to stderr, and supports **runtime dynamic log level adjustment** without restarting the service.

## Basic Usage

```go
import "github.com/rushteam/beauty/pkg/service/logger"

logger.Debug("debug message", "key", "value")
logger.Info("server started", "addr", ":8080")
logger.Warn("slow query", "duration", "2s")
logger.Error("connect failed", "err", err)
```

All log entries carry a `beauty` group prefix for easy filtering in log systems.

## Dynamic Log Level

### Programmatic

```go
import (
    "log/slog"
    "github.com/rushteam/beauty/pkg/service/logger"
)

// Set by level constant
logger.SetLevel(slog.LevelDebug)

// Set by name (case-insensitive)
logger.SetLevelByName("debug")  // debug / info / warn / error
logger.SetLevelByName("INFO")

// Read current level
current := logger.GetLevel()
```

### HTTP Endpoint

Mount `logger.LevelHandler()` on any route:

```go
mux.Handle("/debug/loglevel", logger.LevelHandler())
```

Endpoint reference:

```
# Query current level
GET /debug/loglevel
→ {"level":"info"}

# Change level (takes effect without restart)
PUT /debug/loglevel
Body: {"level":"debug"}
→ {"level":"debug"}
```

Error responses return HTTP 400:

```json
{"error": "unknown log level \"trace\": must be debug, info, warn or error"}
```

## Recommended Integration

Mount the loglevel endpoint in the webserver; in production, protect it with an auth middleware:

```go
r := chi.NewRouter()
r.Handle("/debug/loglevel", logger.LevelHandler())

app := beauty.New(
    beauty.WithWebServer(":8080", r),
)
```
