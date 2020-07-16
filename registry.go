package mojito

//RegistryOptions ..
type RegistryOptions struct{}

//Registry ..
type Registry interface {
	Register(ServiceOptions)
	Deregister(ServiceOptions)
}

//Registry ..
// func Registry(opt mojito.ServiceOptions) {
// 	// DefaultRegistry.
// }
