package grpcgw

import "github.com/grpc-ecosystem/grpc-gateway/v2/runtime"

func New() *runtime.ServeMux {
	return runtime.NewServeMux()
}
