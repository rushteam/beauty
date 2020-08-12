package mysql

import (
	"github.com/rushteam/beauty/pkg/config"
	"github.com/rushteam/beauty/pkg/log"
	"github.com/rushteam/gosql"
	"go.uber.org/zap"
)

//ServiceKind ..
const ServiceKind = "client.mysql"

//Engine ..
type Engine struct {
	Name string
	gosql.Cluster
	Dsn []string
}

//Build create a web service with the name
func Build(name string) (*Engine, error) {
	s := &Engine{
		Name: name,
	}
	if conf, err := config.New(config.Env(), name); err == nil {
		s.Dsn = conf.GetStringSlice(ServiceKind + ".dsn")
	} else {
		log.Warn("no config file...", zap.String("kind", ServiceKind), zap.String("name", name))
	}
	var opts []gosql.PoolClusterOpts
	for _, dsn := range s.Dsn {
		opts = append(opts, gosql.AddDb("mysql", dsn))
	}
	s.Cluster = gosql.NewCluster(opts...)
	return s, nil
}
