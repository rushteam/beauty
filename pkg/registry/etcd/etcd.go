package etcd

import (
	"context"
	"errors"
	"hash"
	"sync"
	"time"

	"github.com/micro/go-micro/registry"
	"github.com/micro/go-micro/v2/logger"
	"github.com/rushteam/mojito"
	"go.etcd.io/etcd/clientv3"
	"go.etcd.io/etcd/etcdserver/api/v3rpc/rpctypes"
)

var (
	prefix = "/micro/registry/"
)

type etcdRegistry struct {
	sync.RWMutex
	client *clientv3.Client
	leases map[string]clientv3.LeaseID
	opts   struct {
		Timeout time.Duration
	}
}

//NewRegistry ..
func NewRegistry() (mojito.Registry, error) {
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
func (e *etcdRegistry) Register(s mojito.ServiceOptions) error {
	nodeKey := ""
	e.RLock()
	leaseID, ok := e.leases[s.Name()+s.ID()]
	e.RUnlock()
	if !ok {
		// missing lease, check if the key exists
		ctx, cancel := context.WithTimeout(context.Background(), e.opts.Timeout)
		defer cancel()
		// look for the existing key
		rsp, err := e.client.Get(ctx, nodeKey, clientv3.WithSerializable())
		if err != nil {
			return err
		}
		// get the existing lease
		for _, kv := range rsp.Kvs {
			if kv.Lease > 0 {
				leaseID = clientv3.LeaseID(kv.Lease)
				// decode the existing node
				srv := decode(kv.Value)
				if srv == nil || len(srv.Nodes) == 0 {
					continue
				}

				// create hash of service; uint64
				h, err := hash.Hash(srv.Nodes[0], nil)
				if err != nil {
					continue
				}

				// save the info
				e.Lock()
				e.leases[s.Name+node.Id] = leaseID
				e.register[s.Name+node.Id] = h
				e.Unlock()

				break
			}
		}
	}
}
func (e *etcdRegistry) Deregister(info mojito.ServiceOptions) {

}
func (e *etcdRegistry) registerNode(s *registry.Service, node *registry.Node, opts ...registry.RegisterOption) error {
		// get the existing lease
		for _, kv := range rsp.Kvs {
			if kv.Lease > 0 {
				leaseID = clientv3.LeaseID(kv.Lease)

				// decode the existing node
				srv := decode(kv.Value)
				if srv == nil || len(srv.Nodes) == 0 {
					continue
				}

				// create hash of service; uint64
				h, err := hash.Hash(srv.Nodes[0], nil)
				if err != nil {
					continue
				}

				// save the info
				e.Lock()
				e.leases[s.Name+node.Id] = leaseID
				e.register[s.Name+node.Id] = h
				e.Unlock()

				break
			}
		}
	}

	var leaseNotFound bool

	// renew the lease if it exists
	if leaseID > 0 {
		if _, err := e.client.KeepAliveOnce(context.TODO(), leaseID); err != nil {
			if err != rpctypes.ErrLeaseNotFound {
				return err
			}
			// lease not found do register
			leaseNotFound = true
		}
	}

	// create hash of service; uint64
	h, err := hash.Hash(node, nil)
	if err != nil {
		return err
	}

	// get existing hash for the service node
	e.Lock()
	v, ok := e.register[s.Name+node.Id]
	e.Unlock()

	// the service is unchanged, skip registering
	if ok && v == h && !leaseNotFound {
		if logger.V(logger.TraceLevel, logger.DefaultLogger) {
			logger.Tracef("Service %s node %s unchanged skipping registration", s.Name, node.Id)
		}
		return nil
	}

	service := &registry.Service{
		Name:      s.Name,
		Version:   s.Version,
		Metadata:  s.Metadata,
		Endpoints: s.Endpoints,
		Nodes:     []*registry.Node{node},
	}

	var options registry.RegisterOptions
	for _, o := range opts {
		o(&options)
	}

	ctx, cancel := context.WithTimeout(context.Background(), e.options.Timeout)
	defer cancel()

	var lgr *clientv3.LeaseGrantResponse
	if options.TTL.Seconds() > 0 {
		// get a lease used to expire keys since we have a ttl
		lgr, err = e.client.Grant(ctx, int64(options.TTL.Seconds()))
		if err != nil {
			return err
		}
	}

	if logger.V(logger.TraceLevel, logger.DefaultLogger) {
		logger.Tracef("Registering %s id %s with lease %v and leaseID %v and ttl %v", service.Name, node.Id, lgr, lgr.ID, options.TTL)
	}
	// create an entry for the node
	if lgr != nil {
		_, err = e.client.Put(ctx, nodePath(service.Name, node.Id), encode(service), clientv3.WithLease(lgr.ID))
	} else {
		_, err = e.client.Put(ctx, nodePath(service.Name, node.Id), encode(service))
	}
	if err != nil {
		return err
	}

	e.Lock()
	// save our hash of the service
	e.register[s.Name+node.Id] = h
	// save our leaseID of the service
	if lgr != nil {
		e.leases[s.Name+node.Id] = lgr.ID
	}
	e.Unlock()

	return nil
}
