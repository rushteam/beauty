package etcdv3

type EtcdConfig struct {
	Endpoints []string
	Username  string
	Password  string
	Namespace string
}
