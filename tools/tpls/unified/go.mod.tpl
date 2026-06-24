module {{.Module}}

go 1.26

require (
	github.com/rushteam/beauty v0.0.0-20260623143921-facef7e964c7
	{{if .EnableWeb}}github.com/go-chi/chi/v5 v5.2.3
	github.com/go-chi/cors v1.2.2
	{{end}}{{if .EnableGrpc}}google.golang.org/grpc v1.81.1
	google.golang.org/protobuf v1.36.8
	{{end}}gopkg.in/yaml.v3 v3.0.1
)
