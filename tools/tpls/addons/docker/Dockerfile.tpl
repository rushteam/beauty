# syntax=docker/dockerfile:1

# ---- 构建阶段 ----
FROM golang:1.26 AS builder
WORKDIR /src

# 先拷贝依赖清单以利用构建缓存
COPY go.* ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags="-s -w -X main.Version=$(cat VERSION 2>/dev/null || echo docker) -X main.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    -o /out/{{.Name}} .

# ---- 运行阶段 ----
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=builder /out/{{.Name}} /app/{{.Name}}
COPY --from=builder /src/config /app/config
{{if .EnableWeb}}EXPOSE 8080
{{end}}{{if .EnableGrpc}}EXPOSE 9090
{{end}}ENTRYPOINT ["/app/{{.Name}}"]
CMD ["--config", "config/dev/app.yaml"]
