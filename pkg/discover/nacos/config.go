package nacos

type Config struct {
	Addr      []string `json:"addr"`
	Cluster   string   `json:"cluster"`
	Namespace string   `json:"namespace"`
	Group     string   `json:"group"`
	Weight    float64  `json:"weight"`
}
