package grpc

import (
	"context"
	"time"

	"github.com/rushteam/beauty/pkg/config"
	"github.com/rushteam/beauty/pkg/log"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/balancer/roundrobin"
)

//ServiceKind ..
const ServiceKind = "client.grpc"

//ServiceConf ..
const ServiceConf = "etcd"

//Client ..
type Client struct {
	Name       string
	Timeout    time.Duration
	ClientConn *grpc.ClientConn
	Traget     string
}

//Build create a web service with the name
func Build(name string) (*Client, error) {
	s := &Client{
		Name: name,
	}
	if conf, err := config.New(config.Env(), name); err == nil {
		s.Timeout = conf.GetDuration(ServiceKind + ".timeout")
		s.Traget = conf.GetString(ServiceKind + ".traget")
	} else {
		log.Warn("no config file...", zap.String("kind", ServiceKind), zap.String("name", name))
	}
	//ectd://[auth/]111,222,333/key
	conn, err := grpc.DialContext(context.TODO(), s.Traget, grpc.WithDefaultServiceConfig(roundrobin.Name), grpc.WithInsecure(), grpc.WithBlock())
	if err != nil {
		return s, err
	}
	s.ClientConn = conn
	return s, nil
}

//Close ..
func (c *Client) Close() error {
	return c.ClientConn.Close()
}
