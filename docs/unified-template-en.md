# Unified Template

> 中文版: [unified-template.md](unified-template.md)

## Overview

The Beauty framework now supports a unified template, which lets users interactively choose which service types to enable instead of using separate templates. This provides more flexibility: a single project can enable HTTP, gRPC, and cron job services at the same time.

## Features

### 1. Interactive Service Selection

When using the `unified` template, the CLI provides an interactive prompt for choosing which service types to enable:

```bash
beauty new my-project --template unified
```

The prompt shows the following options:
- HTTP service (REST API)
- gRPC service (high-performance RPC)
- Cron job service (Cron Jobs)
- Full-stack service (HTTP + gRPC + Cron)
- Custom combination

### 2. Command-Line Flags

Users can also specify the service types to enable directly via command-line flags:

```bash
# enable only the HTTP service
beauty new my-project --template unified --web

# enable HTTP and gRPC services
beauty new my-project --template unified --web --grpc

# enable all services
beauty new my-project --template unified --web --grpc --cron
```

### 3. Smart File Generation

The unified template generates the appropriate files based on the service types the user selects:

- **HTTP service**: generates files under `internal/endpoint/handlers/` and `internal/endpoint/router/`
- **gRPC service**: generates files under `api/`, `internal/endpoint/grpc/`, and `internal/service/`
- **Cron job service**: generates files under `internal/job/`

### 4. Conditional Compilation

The generated code uses Go template conditionals, so code is only included when the corresponding service is enabled:

```go
{{if .EnableWeb}}
// HTTP service code
{{end}}

{{if .EnableGrpc}}
// gRPC service code
{{end}}

{{if .EnableCron}}
// cron job service code
{{end}}
```

## Usage Examples

### Example 1: Create a Full-Stack Service

```bash
beauty new my-fullstack-app --template unified
# choose option 4 (full-stack service)
```

### Example 2: Create an HTTP + gRPC Service

```bash
beauty new my-api-service --template unified --web --grpc
```

### Example 3: Create a Cron-Only Service

```bash
beauty new my-cron-service --template unified --cron
```

## Project Structure

A project generated from the unified template looks like this:

```
my-project/
├── main.go                 # main entry point (conditionally compiled by service type)
├── go.mod                  # Go module file (dependencies included by service type)
├── config/
│   └── dev/
│       └── app.yaml        # config file (settings included by service type)
├── api/                    # gRPC API definitions (only when gRPC is enabled)
│   └── v1/
│       └── user.proto
├── internal/
│   ├── config/
│   │   └── config.go       # config struct (fields included by service type)
│   ├── endpoint/
│   │   ├── handlers/       # HTTP handlers (only when HTTP is enabled)
│   │   ├── router/         # HTTP routes (only when HTTP is enabled)
│   │   └── grpc/           # gRPC services (only when gRPC is enabled)
│   ├── infra/              # infrastructure code
│   ├── job/                # cron jobs (only when Cron is enabled)
│   └── service/            # business services (only when gRPC is enabled)
└── scripts/                # build scripts (only when gRPC is enabled)
    └── generate.sh
```

## Configuration

### HTTP Service Configuration

```yaml
http:
  addr: ":8080"
  read_timeout: "30s"
  write_timeout: "30s"
  idle_timeout: "120s"
```

### gRPC Service Configuration

```yaml
grpc:
  addr: ":9090"
  max_recv_msg_size: 4194304
  max_send_msg_size: 4194304
```

### Cron Job Service Configuration

The cron job service uses default settings and needs no extra configuration.

## Backward Compatibility

The unified template is fully backward compatible with the existing template system:

- `web-service`: still available, equivalent to `unified --web`
- `grpc-service`: still available, equivalent to `unified --grpc`
- `cron-service`: still available, equivalent to `unified --cron`

## Best Practices

1. **Use the unified template for new projects**: use `--template unified` for maximum flexibility
2. **Specify service types explicitly**: in CI/CD environments, use command-line flags instead of the interactive prompt
3. **Enable services on demand**: only enable the service types the project actually needs, avoiding unnecessary dependencies
4. **Use conditional compilation**: use conditionals in your own custom code as well to keep it clean

## Implementation

The unified template is implemented with the following techniques:

1. **Go template conditionals**: conditional statements such as `{{if .EnableWeb}}`
2. **Smart file filtering**: skips files that are not needed for the selected service types
3. **Interactive CLI**: user interaction implemented with `bufio.Reader`
4. **Config-driven**: code generation is controlled by a configuration object

This design keeps the code simple and maintainable while providing maximum flexibility.
