run:
  timeout: 5m

linters:
  enable:
    - govet
    - staticcheck
    - ineffassign
    - unused
    - errcheck

issues:
  exclude-dirs:
    - api/v1
