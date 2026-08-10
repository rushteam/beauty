// Package kitex 提供 Kitex (cloudwego/kitex) Thrift 协议的一等公民集成。
//
// Kitex 是 CloudWeGo 开源的高性能 Go RPC 框架，主要用于 Thrift 协议。
// 本包将 Kitex Server 嵌入为 beauty.Service，纳入框架生命周期管理。
//
// 服务端通过 New 创建 Server，用 Server() 获取底层 kitex server.Server 注册 handler，
// 然后传入 beauty.WithService 即可：
//
//	srv := kitex.New(":8888",
//	    kitex.WithServiceName("example.shop.item"),
//	)
//	itemservice.RegisterService(srv.Server(), new(ItemServiceImpl))
//	app := beauty.New(beauty.WithService(srv))
//
// 客户端通过 NewResolverAdapter 将 beauty 的服务发现适配为 Kitex Resolver：
//
//	adapter := kitex.NewResolverAdapter(beautyDiscovery)
//	client := itemservice.NewClient("example.shop.item",
//	    client.WithResolver(adapter),
//	)
package kitex

import (
	"context"
	"fmt"
	"net"
	"sync"

	kserver "github.com/cloudwego/kitex/server"
	"github.com/rushteam/beauty/pkg/service/discover"
	"github.com/rushteam/beauty/pkg/service/logger"
	"github.com/rushteam/beauty/pkg/utils/addr"
	"github.com/rushteam/beauty/pkg/utils/uuid"
)

var _ discover.Service = (*Server)(nil)

// Server 是基于 Kitex 的 Thrift RPC 服务，实现了 beauty.Service 和
// discover.Service 接口，可直接传入 beauty.WithService 使用。
type Server struct {
	id       string
	name     string
	metadata map[string]string
	addr     string
	ready    chan struct{}

	kitexOpts   []kserver.Option
	kitexServer kserver.Server

	registries       []discover.Registry
	autoDiscover     bool
	serviceDiscovery *ServiceDiscovery
}

// New 创建 Kitex Thrift 服务。addr 为监听地址（如 ":8888"）。
// 使用 Server() 获取底层 kitex server.Server 来注册 handler。
func New(bindAddr string, opts ...Option) *Server {
	s := &Server{
		id:       uuid.New(),
		name:     "kitex-server",
		metadata: map[string]string{"kind": "thrift"},
		addr:     bindAddr,
		ready:    make(chan struct{}),
	}
	for _, o := range opts {
		o(s)
	}

	tcpAddr, err := net.ResolveTCPAddr("tcp", bindAddr)
	if err != nil {
		panic(fmt.Sprintf("kitex: invalid address %q: %v", bindAddr, err))
	}

	kitexOpts := []kserver.Option{
		kserver.WithServiceAddr(tcpAddr),
	}
	kitexOpts = append(kitexOpts, s.kitexOpts...)

	s.kitexServer = kserver.NewServer(kitexOpts...)
	return s
}

// Server 返回底层 kitex server.Server，用于注册 Thrift handler。
// 调用方式：xxxservice.RegisterService(srv.Server(), handler)
func (s *Server) Server() kserver.Server {
	return s.kitexServer
}

// Start 启动服务，阻塞直到 ctx 取消后优雅关闭。实现 beauty.Service 接口。
func (s *Server) Start(ctx context.Context) error {
	var readyOnce sync.Once
	signalReady := func() { readyOnce.Do(func() { close(s.ready) }) }
	defer signalReady()

	signalReady()

	var waitRegistrations func()
	if s.autoDiscover && s.serviceDiscovery != nil {
		s.serviceDiscovery.DiscoverServices(s.kitexServer, s.addr, s.metadata)
		wait, err := s.serviceDiscovery.RegisterAllServices(ctx)
		if err != nil {
			logger.Error("kitex register discovered services failed", "error", err)
		} else {
			waitRegistrations = wait
		}
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("kitex server serve", "addr", s.addr)
		errCh <- s.kitexServer.Run()
	}()

	select {
	case err := <-errCh:
		if err != nil {
			logger.Error("kitex server serve failed", "error", err)
		}
		return err
	case <-ctx.Done():
		logger.Info("kitex server stopping...")
		if err := s.kitexServer.Stop(); err != nil {
			logger.Error("kitex server stop error", "error", err)
		}
		<-errCh
		logger.Info("kitex server stopped")
		if waitRegistrations != nil {
			waitRegistrations()
		}
		return nil
	}
}

// Ready 返回一个 channel，在地址确定后关闭。实现 ReadyNotifier 接口。
func (s *Server) Ready() <-chan struct{} { return s.ready }

func (s *Server) String() string              { return addr.ParseHostPort(s.addr) }
func (s *Server) ID() string                  { return s.id }
func (s *Server) Name() string                { return s.name }
func (s *Server) Kind() string                { return "thrift" }
func (s *Server) Addr() string                { return addr.ParseHostPort(s.addr) }
func (s *Server) Metadata() map[string]string { return s.metadata }
