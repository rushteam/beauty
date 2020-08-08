package registry

import (
	"context"
	"sync"
	"time"

	"github.com/coreos/etcd/clientv3"
	"go.etcd.io/etcd/etcdserver/api/v3rpc/rpctypes"
)

var prefix = "/mojito/service"

var _ Registry = (*etcdRegistry)(nil)

type etcdRegistry struct {
	sync.RWMutex
	client  *clientv3.Client
	leases  map[string]clientv3.LeaseID
	timeout time.Duration
}

//LoadEtcdRegistry ..
func LoadEtcdRegistry() (Registry, error) {
	config := clientv3.Config{
		Endpoints: []string{"http://127.0.0.1:2379"},
	}
	return NewEtcdRegistry(config)
}

//NewEtcdRegistry ..
func NewEtcdRegistry(config clientv3.Config) (Registry, error) {
	e := &etcdRegistry{
		leases:  make(map[string]clientv3.LeaseID),
		timeout: 5 * time.Second,
	}
	client, err := clientv3.New(config)
	if err != nil {
		return nil, err
	}
	e.client = client
	return e, nil
}
func (e *etcdRegistry) loadLeaseID(ctx context.Context, k string, ttl time.Duration) (clientv3.LeaseID, error) {
	e.RLock()
	leaseID, ok := e.leases[k]
	e.RUnlock()
	if ok {
		if _, err := e.client.KeepAliveOnce(ctx, leaseID); err != nil {
			if err == rpctypes.ErrLeaseNotFound {
				goto grant
			}
			return leaseID, err
		}
		return leaseID, nil
	}
grant:
	rsp, err := e.client.Grant(ctx, int64(ttl.Seconds()))
	if err != nil {
		return leaseID, err
	}
	e.Lock()
	e.leases[k] = rsp.ID
	e.Unlock()
	return rsp.ID, nil
}
func (e *etcdRegistry) Register(ctx context.Context, s *Service, ttl time.Duration) error {
	key := s.String()
	val := string(s.Marshal())
	ctxTimeout, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	if ttl.Seconds() > 0 {
		leaseID, err := e.loadLeaseID(ctxTimeout, key, ttl)
		if err != nil {
			return err
		}
		if _, err = e.client.Put(ctxTimeout, key, val, clientv3.WithLease(leaseID)); err != nil {
			return err
		}
		_, err = e.client.KeepAlive(ctx, leaseID)
		return err
	}
	_, err := e.client.Put(ctxTimeout, key, val)
	return err
}

func (e *etcdRegistry) Deregister(ctx context.Context, s *Service) error {
	key := s.String()
	e.Lock()
	leaseID, ok := e.leases[key]
	if ok {
		delete(e.leases, key)
	}
	e.Unlock()
	if ok {
		e.client.Revoke(ctx, leaseID)
	}
	return nil
}

func (e *etcdRegistry) Discover(ctx context.Context, namespace, kind, name string) ([]*Service, error) {
	var serviceNodes []*Service
	key := String(namespace, kind, name)
	rsp, err := e.client.Get(ctx, key, clientv3.WithPrefix())
	if err != nil {
		return serviceNodes, err
	}
	for _, kv := range rsp.Kvs {
		serviceNodes = append(serviceNodes, Unmarshal(kv.Value))
	}
	return serviceNodes, nil
}
func (e *etcdRegistry) Services(ctx context.Context, namespace string) ([]*Service, error) {
	var serviceNodes []*Service
	//todo
	return serviceNodes, nil
}
