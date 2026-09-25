# API Command Protobuf Integration

> 中文版: [api-protobuf-integration.md](api-protobuf-integration.md)

## Overview

The refactored API command now supports protobuf parsing, combined with buf v2 to build and manage protobuf files. This feature provides the following capabilities:

- Automatic detection and parsing of protobuf files
- Parsing `google/api/annotations.proto` with the official [grpc-gateway](https://github.com/grpc-ecosystem/grpc-gateway) packages
- Code generation through the integrated buf v2 toolchain
- Backward compatibility with the traditional api.spec file format
- HTTP annotation parsing

## Features

### 1. Protobuf File Parsing
- Automatically scans the project directory for .proto files
- Parses service definitions, message types and RPC methods with the official [grpc-gateway](https://github.com/grpc-ecosystem/grpc-gateway) packages
- Correctly parses the HTTP annotations from `google/api/annotations.proto`
- Resolves import dependencies

### 2. Buf v2 Integration
- Automatically generates the buf.yaml and buf.gen.yaml configuration files
- Supports code generation (Go, gRPC, grpc-gateway)
- Provides lint checks and formatting
- Supports breaking change detection

### 3. Backward Compatibility
- If protobuf parsing fails, automatically falls back to the traditional api.spec format
- Keeps existing workflows uninterrupted

## Usage

### Basic Usage

```bash
# Parse a protobuf project
beauty api my-project

# Specify the project path
beauty api my-project --path /path/to/project
```

### Project Structure

Recommended project structure:

```
my-project/
├── api/
│   └── service.proto
├── buf.yaml          # auto-generated
├── buf.gen.yaml      # auto-generated
└── api/v1/           # generated code
    ├── service.pb.go
    ├── service_grpc.pb.go
    └── service.pb.gw.go
```

### Example Project

The repository includes a complete example, `examples/protobuf-example/`, showing how to define a service with protobuf, including HTTP options.

### Protobuf File Example

```protobuf
syntax = "proto3";

package v1alpha;

option go_package = "api/v1";

import "google/api/annotations.proto";

service UserService {
  rpc CreateUser (CreateUserRequest) returns (CreateUserResponse) {
    option (google.api.http) = {
      post: "/v1/users"
      body: "*"
    };
  }
}

message CreateUserRequest {
  string name = 1;
  string email = 2;
}

message CreateUserResponse {
  string id = 1;
  string name = 2;
  string email = 3;
}
```

## Configuration

### buf.yaml Configuration

```yaml
version: v2
name: buf.build/example/myrepo
deps:
  - buf.build/googleapis/googleapis
build:
  roots:
    - api
lint:
  use:
    - DEFAULT
breaking:
  use:
    - FILE
```

### buf.gen.yaml Configuration

```yaml
version: v2
managed:
  enabled: true
plugins:
  - name: go
    out: api/v1
    opt: paths=source_relative
  - name: go-grpc
    out: api/v1
    opt: paths=source_relative
  - name: grpc-gateway
    out: api/v1
    opt: paths=source_relative
```

## Sample Output

Sample output after running the command:

```
Parsing protobuf files...
Successfully parsed 1 protobuf file(s):

File: api/service.proto
  Package: v1alpha
  Go package: api/v1
  Services: 1
  Messages: 4
  Service: UserService
    RPC: CreateUser(CreateUserRequest) -> CreateUserResponse
    RPC: GetUser(GetUserRequest) -> GetUserResponse
    RPC: UpdateUser(UpdateUserRequest) -> UpdateUserResponse
    RPC: DeleteUser(DeleteUserRequest) -> DeleteUserResponse
    RPC: ListUsers(ListUsersRequest) -> ListUsersResponse
  Message: CreateUserRequest
    Field: string name 1
    Field: string email 2
    Field: int32 age 3
  Message: CreateUserResponse
    Field: string id 1
    Field: string name 2
    Field: string email 3
    Field: int32 age 4
    Field: string created_at 5

Generating code...
Code generation complete!
```

## Requirements

- Go 1.19+
- buf CLI
- protoc compiler (managed automatically by buf)

## Installing buf

```bash
# macOS
brew install buf

# Linux/Windows
curl -sSL "https://github.com/bufbuild/buf/releases/latest/download/buf-$(uname -s)-$(uname -m)" -o "/usr/local/bin/buf"
chmod +x "/usr/local/bin/buf"
```

## Troubleshooting

### Common Issues

1. **buf is not installed**
   ```
   Error: buf is not installed, please install the buf tool first
   ```
   Solution: install buf following the installation instructions above

2. **Syntax errors in protobuf files**
   ```
   Error: protobuf file check failed
   ```
   Solution: check the syntax of your .proto files and validate them with the `buf lint` command

3. **Missing dependencies**
   ```
   Error: google/api/annotations.proto not found
   ```
   Solution: make sure buf.yaml includes the correct dependencies

### Debug Mode

You can enable verbose output by setting an environment variable:

```bash
export BEAUTY_DEBUG=1
beauty api my-project
```

## Migration Guide

### Migrating from the Traditional api.spec

1. Convert your existing service definitions to protobuf format
2. Create .proto files
3. Run the `beauty api` command to parse and generate code
4. Update your project configuration to use the generated code

### Migration Example

**Traditional format (api.spec):**
```
service UserService {
    @route POST "/users"
    rpc CreateUser(CreateUserRequest) returns (CreateUserResponse)
}
```

**Protobuf format (service.proto):**
```protobuf
service UserService {
  rpc CreateUser (CreateUserRequest) returns (CreateUserResponse) {
    option (google.api.http) = {
      post: "/users"
      body: "*"
    };
  }
}
```
