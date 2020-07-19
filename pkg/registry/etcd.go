package registry

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rushteam/mojito/pkg/service"
	"github.com/rushteam/registry"
	"go.etcd.io/etcd/clientv3"
	"go.etcd.io/etcd/etcdserver/api/v3rpc/rpctypes"
)

var _ Registry = (*etcdRegistry)(nil)

type etcdRegistry struct {
	sync.RWMutex
	client *clientv3.Client
	leases map[string]clientv3.LeaseID
	opts   struct {
		Timeout time.Duration
	}
}

//NewEtcdRegistry ..
func NewEtcdRegistry() (Registry, error) {
	e := &etcdRegistry{
		leases: make(map[string]clientv3.LeaseID),
	}
	config := clientv3.Config{
		Endpoints: []string{"127.0.0.1:2739"},
	}
	client, err := clientv3.New(config)
	if err != nil {
		return nil, err
	}
	e.client = client
	return e, nil
}
func (e *etcdRegistry) Register(s Service) error {
	key := fmt.Sprintf("%v/%v/%v", prefix, s.Name(), s.UUID())
	e.RLock()
	leaseID, ok := e.leases[key]
	e.RUnlock()
	if !ok {
		ctx, cancel := context.WithTimeout(context.Background(), e.opts.Timeout)
		defer cancel()
		rsp, err := e.client.Get(ctx, key, clientv3.WithSerializable())
		if err != nil {
			return err
		}
		for _, kv := range rsp.Kvs {
			if kv.Lease > 0 {
				leaseID = clientv3.LeaseID(kv.Lease)
				// save the info
				e.Lock()
				e.leases[key] = leaseID
				e.Unlock()
				break
			}
		}
	}
	var needPut bool

	// renew the lease if it exists
	if leaseID > 0 {
		if _, err := e.client.KeepAliveOnce(context.TODO(), leaseID); err != nil {
			if err != rpctypes.ErrLeaseNotFound {
				return err
			}
			// lease not found do register
			needPut = true
		}
	}
	if needPut == true {
		info := &registry.Service{
			Name:    s.Name(),
			Version: s.Version(),
			// Metadata: s.Metadata(),
			// Endpoints: s.Endpoints,
			// Nodes:     []*registry.Node{node},
		}
		fmt.Println(info)
		var options registry.RegisterOptions
		for _, o := range opts {
			o(&options)
		}
		ctx, cancel := context.WithTimeout(context.Background(), e.options.Timeout)
		defer cancel()
		var leaseRsp *clientv3.LeaseGrantResponse
		if options.TTL.Seconds() > 0 {
			// get a lease used to expire keys since we have a ttl
			leaseRsp, err = e.client.Grant(ctx, int64(options.TTL.Seconds()))
			if err != nil {
				return err
			}
			if leaseRsp != nil {
				_, err = e.client.Put(ctx, nodePath(service.Name, node.Id), encode(service), clientv3.WithLease(lgr.ID))
			} else {
				_, err = e.client.Put(ctx, nodePath(service.Name, node.Id), encode(service))
			}
		}

	}
}

func (e *etcdRegistry) Deregister(s Service) error {
	return nil
}
