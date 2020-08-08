package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

//Registry ..
type Registry interface {
	Register(ctx context.Context, s *Service, ttl time.Duration) error
	Deregister(ctx context.Context, s *Service) error
	Discover(ctx context.Context, namespace, kind, name string) ([]*Service, error)
	Services(ctx context.Context, namespace string) ([]*Service, error)
}

//Service ..
// type Service interface {
// 	String() string
// 	ID() string
// 	Encode() string
// }

//Service info
type Service struct {
	Namespace string            `json:"namespace"`
	Kind      string            `json:"kind"`
	Name      string            `json:"name"`
	ID        string            `json:"id"`
	Version   string            `json:"version"`
	Address   string            `json:"address"`
	Labels    map[string]string `json:"labels"`
	// *Node
	// Metadata map[string]string `json:"metadata"`
	// Endpoints []*Endpoint       `json:"endpoints"`
	// Nodes     []*Node           `json:"nodes"`
}

//Node info
type Node struct {
	ID      string            `json:"id"`
	Version string            `json:"version"`
	Address string            `json:"address"`
	Labels  map[string]string `json:"labels"`
}

//String ..
func String(namespace, kind, name string) string {
	return fmt.Sprintf("%v/%v/%v/%v/", prefix, namespace, kind, name)
}

//String ..
func (s *Service) String() string {
	return fmt.Sprintf("%v%v", String(s.Namespace, s.Kind, s.Name), s.ID)
}

//Marshal ...
func (s *Service) Marshal() []byte {
	v, _ := json.Marshal(s)
	return v
}

//Unmarshal ..
func Unmarshal(v []byte) *Service {
	var s *Service
	json.Unmarshal(v, s)
	return s
}

//Endpoint ..
// type Endpoint struct {
// 	Name     string            `json:"name"`
// 	Request  *Value            `json:"request"`
// 	Response *Value            `json:"response"`
// 	Metadata map[string]string `json:"metadata"`
// }
