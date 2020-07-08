package mojito

type RegistryOptions struct{}

//Registry ..
type Registry interface {
	Register(info ServiceOptions)
	Deregister(info ServiceOptions)
}

//Registry ..
// func Registry(opt mojito.ServiceOptions) {
// 	// DefaultRegistry.
// }
