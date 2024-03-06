package discover

import "net"

type Endpoint interface {
	ServiceName() string
	Addr() net.Addr
	Weight() int
	Metadata() map[string]string
}

type Registry interface {
	Register(info Endpoint) error
	Deregister(info Endpoint) error
}

type EndpointBasicInfo struct {
	serviceName string
	addr        net.Addr
	weight      int
	metadata    map[string]string
}

func (s EndpointBasicInfo) ServiceName() string {
	return s.serviceName
}
func (s EndpointBasicInfo) Addr() net.Addr {
	return s.addr
}
func (s EndpointBasicInfo) Weight() int {
	return s.weight
}
func (s EndpointBasicInfo) Metadata() map[string]string {
	return s.metadata
}
