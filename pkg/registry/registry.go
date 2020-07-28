package registry

import (
	"context"
	"time"
)

//Registry ..
type Registry interface {
	Register(ctx context.Context, s Service, ttl time.Duration) error
	Deregister(ctx context.Context, s Service) error
}

//Service ..
type Service interface {
	String() string
	ID() string
	Encode() string
	Context() context.Context
}

//Service info
// type Service struct {
// 	Name     string            `json:"name"`
// 	Version  string            `json:"version"`
// 	Metadata map[string]string `json:"metadata"`
// 	// Endpoints []*Endpoint       `json:"endpoints"`
// 	// Nodes     []*Node           `json:"nodes"`
// }
// type Service interface {
// 	String() string
// 	ID() string
// 	Version() string
// 	Encode() string
// }

//Node info
type Node struct {
	ID       string            `json:"id"`
	Address  string            `json:"address"`
	Metadata map[string]string `json:"metadata"`
}

//Endpoint ..
type Endpoint struct {
	Name     string            `json:"name"`
	Request  *Value            `json:"request"`
	Response *Value            `json:"response"`
	Metadata map[string]string `json:"metadata"`
}

//Value ..
type Value struct {
	Name   string   `json:"name"`
	Type   string   `json:"type"`
	Values []*Value `json:"values"`
}
