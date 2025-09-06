version: v2
managed:
  enabled: true
plugins:
  - remote: buf.build/protocolbuffers/go:v1.31.0
    out: api/v1
    opt:
      - paths=source_relative
  - remote: buf.build/grpc/go:v1.2.0
    out: api/v1
    opt:
      - paths=source_relative
