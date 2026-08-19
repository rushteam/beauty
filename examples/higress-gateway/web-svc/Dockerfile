FROM golang:1.26-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY web-svc/ ./web-svc/
RUN CGO_ENABLED=0 go build -o /app ./web-svc/

FROM alpine:3.21
COPY --from=builder /app /app
EXPOSE 8080
ENTRYPOINT ["/app"]
