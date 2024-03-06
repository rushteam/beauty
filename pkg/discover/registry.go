package discover

type Endpoint interface {
	ServiceName() string
}

type Registry interface {
	Register(info Endpoint) error
	Deregister(info Endpoint) error
}
