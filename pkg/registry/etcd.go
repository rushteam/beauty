package registry

import (
	"context"
	"sync"
	"time"

	"github.com/coreos/etcd/clientv3"
	"github.com/coreos/etcd/clientv3/concurrency"
	"github.com/rushteam/beauty/pkg/config"
	"github.com/rushteam/beauty/pkg/log"
)

var prefix = "/beauty/service"

var _ Registry = (*etcdRegistry)(nil)

type etcdRegistry struct {
	sync.RWMutex
	client   *clientv3.Client
	timeout  time.Duration
	sessions map[string]*concurrency.Session
}

//ServiceKind ..
const ServiceKind = "registry.etcd"

//Name ..
const Name = "etcd"

//LoadEtcdRegistry ..
func LoadEtcdRegistry() (Registry, error) {
	var endpoints []string
	if conf, err := config.New(config.Env(), Name); err == nil {
		endpoints = conf.GetStringSlice(ServiceKind + ".endpoints")
	} else {
		log.Warn("no config file...")
		endpoints = []string{"http://127.0.0.1:2379"}
	}

	config := clientv3.Config{
		Endpoints: endpoints,
	}
	return NewEtcdRegistry(config)
}

//NewEtcdRegistry ..
func NewEtcdRegistry(config clientv3.Config) (Registry, error) {
	e := &etcdRegistry{
		timeout:  5 * time.Second,
		sessions: make(map[string]*concurrency.Session),
	}
	client, err := clientv3.New(config)
	if err != nil {
		return nil, err
	}
	e.client = client
	return e, nil
}
func (reg *etcdRegistry) getSession(k string, opts ...concurrency.SessionOption) (*concurrency.Session, error) {
	reg.RLock()
	sess, ok := reg.sessions[k]
	reg.RUnlock()
	if ok {
		return sess, nil
	}
	sess, err := concurrency.NewSession(reg.client)
	if err != nil {
		return sess, err
	}
	reg.Lock()
	reg.sessions[k] = sess
	reg.Unlock()
	return sess, nil
}

func (reg *etcdRegistry) delSession(k string) error {
	if ttl := reg.Config.ServiceTTL.Seconds(); ttl > 0 {
		reg.rmu.RLock()
		sess, ok := reg.sessions[k]
		reg.rmu.RUnlock()
		if ok {
			reg.rmu.Lock()
			delete(reg.sessions, k)
			reg.rmu.Unlock()
			if err := sess.Close(); err != nil {
				return err
			}
		}
	}
	return nil
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
	key := naming(namespace, kind, name)
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
