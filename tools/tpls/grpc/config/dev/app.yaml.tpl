app: {{.Name}}
version: "1.0.0"
description: "{{.Name}} gRPC service"

grpc:
  addr: ":9090"
  timeout: "30s"

log:
  level: "info"
  format: "json"
  output: "stdout"

registry:
  type: "etcd"
  endpoints:
    - "127.0.0.1:2379"
  config:
    prefix: "/beauty"
    ttl: "10s"

middleware:
  auth:
    enabled: false
    type: "jwt"
    secret: "your-secret-key"
  
  rate_limit:
    enabled: true
    rate: 100.0
    burst: 200
  
  timeout:
    enabled: true
    timeout: "30s"
    slow_threshold: "5s"
  
  circuit_breaker:
    enabled: true
    max_requests: 5
    interval: "1m"
    timeout: "30s"
