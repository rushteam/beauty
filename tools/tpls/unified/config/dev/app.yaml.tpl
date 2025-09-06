app: {{.Name}}
version: "1.0.0"
description: "{{.Name}} service"

{{if .EnableWeb}}http:
  addr: ":8080"
  read_timeout: "30s"
  write_timeout: "30s"
  idle_timeout: "120s"
{{end}}

{{if .EnableGrpc}}grpc:
  addr: ":9090"
  max_recv_msg_size: 4194304
  max_send_msg_size: 4194304
{{end}}

log:
  level: "info"
  format: "json"
  output: "stdout"

database:
  driver: "postgres"
  dsn: "postgres://user:password@localhost/{{.Name}}?sslmode=disable"

redis:
  addr: "localhost:6379"
  password: ""
  db: 0

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
    skip_paths:
      - "/health"
      - "/metrics"
      - "/swagger/*"
  
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
