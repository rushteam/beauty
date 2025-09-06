module {{.Module}}

go 1.21

require (
	github.com/rushteam/beauty v0.0.0-20250906053003-0c0f2052367e
	{{if .EnableWeb}}github.com/go-chi/chi/v5 v5.2.3
	github.com/go-chi/cors v1.2.2
	{{end}}{{if .EnableGrpc}}google.golang.org/grpc v1.60.1
	google.golang.org/protobuf v1.32.0
	{{end}}gopkg.in/yaml.v3 v3.0.1
)
