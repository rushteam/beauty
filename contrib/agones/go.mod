module github.com/rushteam/beauty/contrib/agones

go 1.26.5

require agones.dev/agones v1.60.0

require github.com/rushteam/beauty v0.0.0

require (
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.30.0 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	golang.org/x/net v0.57.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.40.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260803160001-6ac0973c030d // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260803160001-6ac0973c030d // indirect
	google.golang.org/grpc v1.83.0 // indirect
	google.golang.org/protobuf v1.36.12-0.20260120151049-f2248ac996af // indirect
)

replace github.com/rushteam/beauty => ../..
