package naming

import (
	"encoding/json"

	"google.golang.org/grpc/attributes"
)

//Node info
type Node struct {
	// ID      string `json:"id"`
	// Version string `json:"ver"`
	//grpc Address
	ServerName string                 `json:"service"`
	Addr       string                 `json:"addr"`
	Attributes *attributes.Attributes `json:"attributes"`
}

//Unmarshal ..
func (n *Node) Unmarshal(v []byte) error {
	return json.Unmarshal(v, n)
}
