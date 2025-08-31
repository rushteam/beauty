package grpcclient

import (
	"math"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"google.golang.org/grpc/resolver"
)

const (
	BalancerName    = "p2c_ewma"
	forcePick       = int64(time.Second) // 默认上次被选择的间隔时间
	initSuccess     = 1000
	throttleSuccess = initSuccess / 2
	decayTime       = int64(time.Second * 10)
)

func newBuilder() balancer.Builder {
	return base.NewBalancerBuilder(BalancerName, new(p2cEwmaPickerBuilder), base.Config{HealthCheck: true})
}

func init() {
	balancer.Register(newBuilder())
}

var initTime = time.Now().AddDate(-1, -1, -1)

func Now() time.Duration {
	return time.Since(initTime)
}

type conn struct {
	addr     resolver.Address
	conn     balancer.SubConn
	lag      uint64 // 用来保存 ewma 值
	inflight int64  // 用在保存当前正在使用此连接的请求总数
	success  uint64 // 用来标识一段时间内此连接的健康状态
	requests int64  // 用来保存请求总数
	last     int64  // 用来保存上一次请求耗时, 计算 ewma 值
	pick     int64  // 保存上一次被选中的时间点
}

// 计算负载率
func (s *conn) load() int64 {
	// 获取这个服务的 ewma, 加 1 的目的是为了防止lag等于0
	lag := int64(math.Sqrt(float64(atomic.LoadUint64(&s.lag) + 1)))
	// 获取是不是正在运行
	load := lag * (atomic.LoadInt64(&s.inflight) + 1)

	if load == 0 {
		return 1<<31 - 1
	}
	return load
}

func (s *conn) healthy() bool {
	return atomic.LoadUint64(&s.success) > throttleSuccess
}

// impl grpc/balancer/base.PickerBuilder
type p2cEwmaPickerBuilder struct{}

func (b *p2cEwmaPickerBuilder) Build(buildInfo base.PickerBuildInfo) balancer.Picker {
	if len(buildInfo.ReadySCs) == 0 {
		return base.NewErrPicker(balancer.ErrNoSubConnAvailable)
	}
	//保存所有的连接
	var allConn []*conn
	for k, v := range buildInfo.ReadySCs {
		allConn = append(allConn, &conn{
			addr:    v.Address,
			conn:    k,
			success: initSuccess,
		})
	}

	return &picker{
		conns: allConn,
		rand:  rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

type picker struct {
	conns []*conn
	rand  *rand.Rand
	lock  sync.Mutex
}

// 这里主要做负载均衡的
func (p *picker) Pick(info balancer.PickInfo) (result balancer.PickResult, err error) {
	p.lock.Lock()
	defer p.lock.Unlock()
	var chosen *conn
	switch len(p.conns) {
	case 0:
		return result, balancer.ErrNoSubConnAvailable
	case 1:
		chosen = p.pick(p.conns[0], nil)
	case 2:
		chosen = p.pick(p.conns[0], p.conns[1])
	default:
		var node1, node2 *conn
		for i := 0; i < 3; i++ {
			a := p.rand.Intn(len(p.conns))
			b := p.rand.Intn(len(p.conns) - 1)
			if b > a { // 说明选择的范围比较小
				b++
			}
			node1 = p.conns[a]
			node2 = p.conns[b]
			if node1.healthy() && node2.healthy() { // 说明上次成功请求耗时特别短, 优先选择这两个
				break
			}
		}

		chosen = p.pick(node1, node2)
	}
	// 正在处理请求数+1
	atomic.AddInt64(&chosen.inflight, 1)
	// 处理的总请求数+1
	atomic.AddInt64(&chosen.requests, 1)
	res := balancer.PickResult{
		SubConn: chosen.conn,
		Done:    p.buildDoneFunc(chosen),
	}
	return res, nil
}

// buildDoneFunc 调用完服务端接口会调用这个方法
func (p *picker) buildDoneFunc(s *conn) func(info balancer.DoneInfo) {
	start := int64(Now())
	return func(info balancer.DoneInfo) {
		// 执行完了 把正在执行的总数减 1
		atomic.AddInt64(&s.inflight, -1)
		now := Now()
		// 存储当前执行完的时间节点
		last := atomic.SwapInt64(&s.last, int64(now))
		// 上一次请求结束距离这次请求结束的时间差
		td := int64(now) - last
		if td < 0 {
			td = 0
		}
		// 这个计算公式是 牛顿定律中的衰减函数模型
		w := math.Exp(float64(-td) / float64(decayTime))
		lag := int64(now) - start
		if lag < 0 { // 请求没有花费时间就执行完了，按理是不可能的
			lag = 0
		}
		olag := atomic.LoadUint64(&s.lag)
		if olag == 0 {
			w = 0
		}
		atomic.StoreUint64(&s.lag, uint64(float64(olag)*w+float64(lag)*(1-w)))
		success := 1000
		if info.Err != nil { // 如果有失败这次的值作废
			success = 0
		}

		osucc := atomic.LoadUint64(&s.success)
		// 存储成功的 ewma
		atomic.StoreUint64(&s.success, uint64(float64(osucc)*w+float64(success)*(1-w)))
	}
}

func (p *picker) pick(c1, c2 *conn) *conn {
	start := int64(Now())
	if c2 == nil {
		atomic.StoreInt64(&c1.pick, start)
		return c1
	}

	if c1.load() > c2.load() {
		c1, c2 = c2, c1
	}

	pick := atomic.LoadInt64(&c2.pick)
	if start-pick > forcePick && atomic.CompareAndSwapInt64(&c2.pick, pick, start) {
		return c2
	}
	atomic.StoreInt64(&c1.pick, start)
	return c1
}
