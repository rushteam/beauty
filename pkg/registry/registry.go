package registry

var (
	prefix = "/mojito/registry/"
)

//Options ..
type Options struct{}

//Registry ..
type Registry interface {
	Register(s Service) error
	Deregister(s Service) error
}

//Service info
// type Service struct {
// 	Name     string            `json:"name"`
// 	Version  string            `json:"version"`
// 	Metadata map[string]string `json:"metadata"`
// 	// Endpoints []*Endpoint       `json:"endpoints"`
// 	// Nodes     []*Node           `json:"nodes"`
// }
type Service interface {
	Name() string
	UUID() string
	Version() string
	Encode() string
}

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

func encode() string {
	return ""
}

//Registry ..
// func Registry(opt mojito.ServiceOptions) {
// 	// DefaultRegistry.
// }
