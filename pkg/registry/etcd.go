package registry

import (
	"context"
	"sync"
	"time"

	"github.com/coreos/etcd/clientv3"
	"github.com/coreos/etcd/clientv3/concurrency"
	"github.com/coreos/etcd/mvcc/mvccpb"
	"github.com/rushteam/beauty/pkg/config"
)

var prefix = "/beauty/service"

var _ Registry = (*EtcdRegistry)(nil)

//EtcdRegistry ..
type EtcdRegistry struct {
	sync.RWMutex
	Client   *clientv3.Client
	timeout  time.Duration
	sessions map[string]*concurrency.Session
}

//ServiceKind ..
const ServiceKind = "registry.etcd"

//Name ..
const Name = "etcd"

//Build ..
func Build() (*EtcdRegistry, error) {
	endpoints := []string{"http://127.0.0.1:2379"}
	if conf, err := config.New(config.Env(), Name); err == nil {
		endpoints = conf.GetStringSlice(ServiceKind + ".endpoints")
	}
	config := clientv3.Config{
		Endpoints: endpoints,
	}
	client, err := clientv3.New(config)
	if err != nil {
		return nil, err
	}
	e := &EtcdRegistry{
		timeout:  5 * time.Second,
		sessions: make(map[string]*concurrency.Session),
		Client:   client,
	}
	return e, nil
}

func (e *EtcdRegistry) getSession(k string, opts ...concurrency.SessionOption) (*concurrency.Session, error) {
	e.RLock()
	sess, ok := e.sessions[k]
	e.RUnlock()
	if ok {
		return sess, nil
	}
	sess, err := concurrency.NewSession(e.Client)
	if err != nil {
		return sess, err
	}
	e.Lock()
	e.sessions[k] = sess
	e.Unlock()
	return sess, nil
}

func (e *EtcdRegistry) delSession(k string) error {
	e.RLock()
	sess, ok := e.sessions[k]
	e.RUnlock()
	if ok {
		e.Lock()
		delete(e.sessions, k)
		e.Unlock()
		if err := sess.Close(); err != nil {
			return err
		}
	}
	return nil
}

//Register ...
func (e *EtcdRegistry) Register(ctx context.Context, s *Service, ttl time.Duration) error {
	key := s.String()
	val := string(s.Marshal())
	ctxTimeout, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	var opts []clientv3.OpOption
	if ttl := ttl.Seconds(); ttl > 0 {
		sess, err := e.getSession(key, concurrency.WithTTL(int(ttl)))
		if err != nil {
			return err
		}
		opts = append(opts, clientv3.WithLease(sess.Lease()))
	}
	_, err := e.Client.Put(ctxTimeout, key, val, opts...)
	return err
}

//Deregister ..
func (e *EtcdRegistry) Deregister(ctx context.Context, s *Service) error {
	key := s.String()
	if err := e.delSession(key); err != nil {
		return err
	}
	_, err := e.Client.Delete(ctx, key)
	return err
}

//Discover ..
func (e *EtcdRegistry) Discover(ctx context.Context, naming string) (<-chan map[string]*Node, error) {
	var rspChan = make(chan map[string]*Node, 1)
	rsp, err := e.Client.Get(ctx, naming, clientv3.WithPrefix())
	if err != nil {
		return rspChan, err
	}
	go func() {
		var nodes = make(map[string]*Node, 0)
		for _, kv := range rsp.Kvs {
			node := &Node{}
			if err := node.Unmarshal(kv.Value); err == nil {
				k := string(kv.Key)[len(naming):]
				nodes[k] = node
			}
		}
		rspChan <- nodes
		wch := e.Client.Watch(ctx, naming, clientv3.WithPrefix())
		for {
			select {
			case rsp, ok := <-wch:
				if !ok {
					return
				}
				for _, ev := range rsp.Events {
					delete(nodes, string(ev.Kv.Value))
					if ev.Type == mvccpb.PUT {
						node := &Node{}
						if err := node.Unmarshal(ev.Kv.Value); err == nil {
							k := string(ev.Kv.Value)[len(naming):]
							nodes[k] = node
						}
					}
				}
				rspChan <- nodes
				//using chan close to return this code be not important
				// case <-ctx.Done():
				// 	fmt.Println("close with done")
				// 	return
			}
		}
	}()
	return rspChan, nil
}

//Services ..
func (e *EtcdRegistry) Services(ctx context.Context, naming string) ([]*Service, error) {
	var serviceNodes []*Service
	//todo
	return serviceNodes, nil
}
