package registry

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.etcd.io/etcd/clientv3"
	"go.etcd.io/etcd/etcdserver/api/v3rpc/rpctypes"
)

var prefix = "/mojito/service/"

var _ Registry = (*etcdRegistry)(nil)

type etcdRegistry struct {
	sync.RWMutex
	client *clientv3.Client
	leases map[string]clientv3.LeaseID
	opts   struct {
		Timeout  time.Duration
		leaseTTL time.Duration
	}
	config clientv3.Config
}

//NewEtcdRegistry ..
func NewEtcdRegistry(endpoints ...string) (Registry, error) {
	if len(endpoints) == 0 {
		endpoints = []string{"127.0.0.1:2739"}
	}
	e := &etcdRegistry{
		leases: make(map[string]clientv3.LeaseID),
	}
	client, err := clientv3.New(clientv3.Config{
		Endpoints: endpoints,
	})
	if err != nil {
		return nil, err
	}
	e.client = client
	return e, nil
}
func (e *etcdRegistry) loadLeaseID(k string) (clientv3.LeaseID, error) {
	//from struct cache
	e.RLock()
	leaseID, ok := e.leases[k]
	e.RUnlock()
	if ok {
		return leaseID, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), e.opts.Timeout)
	defer cancel()
	//from etcd
	if rsp, err := e.client.Get(ctx, k, clientv3.WithSerializable()); err == nil {
		for _, kv := range rsp.Kvs {
			if kv.Lease > 0 {
				leaseID = clientv3.LeaseID(kv.Lease)
				e.Lock()
				e.leases[k] = leaseID
				e.Unlock()
				break
			}
		}
		if _, err := e.client.KeepAliveOnce(context.TODO(), leaseID); err != nil {
			if err != rpctypes.ErrLeaseNotFound {
				return leaseID, nil
			}
		}
	}
	//new lease
	rsp, err := e.client.Grant(ctx, int64(e.opts.leaseTTL.Seconds()))
	if err != nil {
		return leaseID, err
	}
	e.Lock()
	e.leases[k] = rsp.ID
	e.Unlock()
	return rsp.ID, nil
}

func (e *etcdRegistry) Register(s Service) error {
	key := fmt.Sprintf("%v/%v/%v", prefix, s.String(), s.ID())
	ctx, cancel := context.WithTimeout(context.Background(), e.opts.Timeout)
	defer cancel()
	if e.opts.leaseTTL.Seconds() > 0 {
		leaseID, err := e.loadLeaseID(key)
		if err != nil {
			return err
		}
		// info := &registry.Service{
		// 	Name:    s.Name(),
		// 	Version: s.Version(),
		// 	// Metadata: s.Metadata(),
		// 	// Endpoints: s.Endpoints,
		// 	// Nodes:     []*registry.Node{node},
		// }
		_, err = e.client.Put(ctx, key, s.Encode(), clientv3.WithLease(leaseID))
		return err
	}
	_, err := e.client.Put(ctx, key, s.Encode())
	return err
}

func (e *etcdRegistry) Deregister(s Service) error {
	return nil
}
