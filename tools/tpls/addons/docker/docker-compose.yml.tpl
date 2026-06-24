services:
  {{.Name}}:
    build: .
    image: {{.Name}}:latest
    restart: unless-stopped
{{if or .EnableWeb .EnableGrpc}}    ports:
{{if .EnableWeb}}      - "8080:8080"
{{end}}{{if .EnableGrpc}}      - "9090:9090"
{{end}}{{end}}    environment:
      - OTEL_EXPORTER_OTLP_ENDPOINT=http://jaeger:4318
    depends_on:
      - etcd
      - jaeger

  etcd:
    image: quay.io/coreos/etcd:v3.5.17
    command:
      - etcd
      - --advertise-client-urls=http://0.0.0.0:2379
      - --listen-client-urls=http://0.0.0.0:2379
    ports:
      - "2379:2379"

  jaeger:
    image: jaegertracing/all-in-one:1.62.0
    environment:
      - COLLECTOR_OTLP_ENABLED=true
    ports:
      - "16686:16686" # Jaeger UI
      - "4317:4317"   # OTLP gRPC
      - "4318:4318"   # OTLP HTTP
