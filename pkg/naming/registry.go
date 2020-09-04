package naming

import (
	"context"
	"sync"
	"time"

	"github.com/coreos/etcd/clientv3"
	"github.com/coreos/etcd/clientv3/concurrency"

	"github.com/rushteam/beauty/pkg/config"
)

var prefix = "/beauty/service"

// var _ Registry = (*EtcdRegistry)(nil)

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
	return New(endpoints)
}

//New ..
func New(endpoints []string) (*EtcdRegistry, error) {
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

//Subscriber ..
func (e *EtcdRegistry) Subscriber(ctx context.Context, key string) (<-chan map[string][]byte, error) {
	var rspChan = make(chan map[string][]byte, 1)
	rsp, err := e.Client.Get(ctx, key, clientv3.WithPrefix())
	if err != nil {
		return rspChan, err
	}
	go func() {
		var nodes = make(map[string][]byte, 0)
		for _, kv := range rsp.Kvs {
			k := string(kv.Key)
			nodes[k] = kv.Value
		}
		rspChan <- nodes
		wch := e.Client.Watch(ctx, key, clientv3.WithPrefix())
		for {
			select {
			case rsp, ok := <-wch:
				if !ok {
					close(rspChan)
					break
				}
				for _, ev := range rsp.Events {
					k := string(ev.Kv.Key)
					switch ev.Type {
					case clientv3.EventTypePut:
						nodes[k] = ev.Kv.Value
					case clientv3.EventTypeDelete:
						delete(nodes, k)
					}
				}
				rspChan <- nodes
			}
		}
	}()
	return rspChan, nil
}
