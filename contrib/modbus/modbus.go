// Package modbus 是基于 grid-x/modbus 的 Modbus 采集器,作为**独立 Go 模块**发布
// (github.com/rushteam/beauty/contrib/modbus),不进 beauty 核心依赖图。
//
// 定位:Modbus Master(主站)——周期性轮询 Slave(从站)寄存器,将采集数据交给用户 Handler
// 处理。Modbus 是请求-响应协议(非 pub/sub),因此不直接实现 mq.Publisher/Subscriber,
// 而是提供 MQBridge 将采集数据桥接到任意 mq.Publisher(MQTT/Kafka/NATS)。
//
// 支持传输:
//   - Modbus TCP (最常用,工业以太网)
//   - Modbus RTU over TCP (RS-485 转 TCP 网关)
//
// 作为 beauty.Service 运行:Start 后按配置周期轮询,ctx 取消优雅停止。
package modbus

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/grid-x/modbus"
)

// RegisterType 寄存器类型。
type RegisterType int

const (
	Coil            RegisterType = iota // 线圈(读写,1-bit)
	DiscreteInput                       // 离散输入(只读,1-bit)
	HoldingRegister                     // 保持寄存器(读写,16-bit)
	InputRegister                       // 输入寄存器(只读,16-bit)
)

func (r RegisterType) String() string {
	switch r {
	case Coil:
		return "coil"
	case DiscreteInput:
		return "discrete_input"
	case HoldingRegister:
		return "holding_register"
	case InputRegister:
		return "input_register"
	default:
		return "unknown"
	}
}

// RegisterGroup 描述一组连续寄存器的读取规则。
type RegisterGroup struct {
	Type     RegisterType // 寄存器类型
	Start    uint16       // 起始地址
	Quantity uint16       // 数量
}

// DeviceConfig 描述一个 Modbus 从站设备的采集配置。
type DeviceConfig struct {
	// Name 是设备名(日志/标识用)。
	Name string
	// SlaveID 从站地址(1~247)。
	SlaveID byte
	// Address 连接地址,格式 "host:port"(TCP)。
	Address string
	// PollInterval 轮询间隔。
	PollInterval time.Duration
	// Timeout 单次请求超时(默认 1s)。
	Timeout time.Duration
	// Registers 要采集的寄存器组。
	Registers []RegisterGroup
}

// DataPoint 表示一次采集到的数据点。
type DataPoint struct {
	DeviceName string        // 设备名
	SlaveID    byte          // 从站地址
	Register   RegisterGroup // 寄存器信息
	Raw        []byte        // 原始字节
	Timestamp  time.Time     // 采集时间
}

// Handler 处理采集到的数据点(一次轮询产生一批数据)。
type Handler func(ctx context.Context, points []DataPoint) error

// CollectorOption 配置 Collector。
type CollectorOption func(*Collector)

// WithCollectorName 设置采集器名(日志/beauty.Service.String)。
func WithCollectorName(name string) CollectorOption {
	return func(c *Collector) { c.name = name }
}

// Collector 是 Modbus 轮询采集器,满足 beauty.Service / ReadyNotifier。
type Collector struct {
	name    string
	devices []DeviceConfig
	handler Handler
	ready   chan struct{}
}

// NewCollector 创建采集器。
func NewCollector(devices []DeviceConfig, handler Handler, opts ...CollectorOption) *Collector {
	c := &Collector{
		name:    "modbus.Collector",
		devices: devices,
		handler: handler,
		ready:   make(chan struct{}),
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Start 启动所有设备的轮询,ctx 取消时停止。满足 beauty.Service。
func (c *Collector) Start(ctx context.Context) error {
	close(c.ready)
	slog.Info("modbus collector started", "devices", len(c.devices))

	var wg sync.WaitGroup
	for i := range c.devices {
		dev := c.devices[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.pollDevice(ctx, dev)
		}()
	}
	wg.Wait()
	slog.Info("modbus collector stopped")
	return nil
}

// Ready 满足 beauty.ReadyNotifier。
func (c *Collector) Ready() <-chan struct{} { return c.ready }

// String 满足 beauty.Service。
func (c *Collector) String() string { return c.name }

func (c *Collector) pollDevice(ctx context.Context, dev DeviceConfig) {
	timeout := dev.Timeout
	if timeout <= 0 {
		timeout = time.Second
	}
	interval := dev.PollInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			points, err := c.readDevice(dev, timeout)
			if err != nil {
				slog.Warn("modbus: read device failed",
					"device", dev.Name, "slave", dev.SlaveID, "addr", dev.Address, "err", err)
				continue
			}
			if len(points) > 0 {
				if err := c.handler(ctx, points); err != nil {
					slog.Warn("modbus: handler error", "device", dev.Name, "err", err)
				}
			}
		}
	}
}

func (c *Collector) readDevice(dev DeviceConfig, timeout time.Duration) ([]DataPoint, error) {
	handler := modbus.NewTCPClientHandler(dev.Address)
	handler.Timeout = timeout
	handler.SlaveID = dev.SlaveID

	ctx := context.Background()
	if err := handler.Connect(ctx); err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer handler.Close()

	client := modbus.NewClient(handler)
	now := time.Now()

	var points []DataPoint
	for _, reg := range dev.Registers {
		raw, err := readRegister(ctx, client, reg)
		if err != nil {
			slog.Debug("modbus: read register failed",
				"device", dev.Name, "type", reg.Type.String(),
				"start", reg.Start, "quantity", reg.Quantity, "err", err)
			continue
		}
		points = append(points, DataPoint{
			DeviceName: dev.Name,
			SlaveID:    dev.SlaveID,
			Register:   reg,
			Raw:        raw,
			Timestamp:  now,
		})
	}
	return points, nil
}

func readRegister(ctx context.Context, client modbus.Client, reg RegisterGroup) ([]byte, error) {
	switch reg.Type {
	case Coil:
		return client.ReadCoils(ctx, reg.Start, reg.Quantity)
	case DiscreteInput:
		return client.ReadDiscreteInputs(ctx, reg.Start, reg.Quantity)
	case HoldingRegister:
		return client.ReadHoldingRegisters(ctx, reg.Start, reg.Quantity)
	case InputRegister:
		return client.ReadInputRegisters(ctx, reg.Start, reg.Quantity)
	default:
		return nil, fmt.Errorf("unknown register type: %d", reg.Type)
	}
}

// === 写入操作 ===

// Writer 提供对 Modbus 从站的写入能力(下发控制指令)。
type Writer struct {
	address string
	slaveID byte
	timeout time.Duration
}

// NewWriter 创建 Modbus 写入器。
func NewWriter(address string, slaveID byte, opts ...WriterOption) *Writer {
	w := &Writer{address: address, slaveID: slaveID, timeout: time.Second}
	for _, o := range opts {
		o(w)
	}
	return w
}

// WriterOption 配置 Writer。
type WriterOption func(*Writer)

// WithWriterTimeout 设置写入超时。
func WithWriterTimeout(d time.Duration) WriterOption {
	return func(w *Writer) { w.timeout = d }
}

// WriteSingleCoil 写单个线圈(0xFF00=ON, 0x0000=OFF)。
func (w *Writer) WriteSingleCoil(addr uint16, value bool) error {
	handler := modbus.NewTCPClientHandler(w.address)
	handler.Timeout = w.timeout
	handler.SlaveID = w.slaveID
	ctx := context.Background()
	if err := handler.Connect(ctx); err != nil {
		return fmt.Errorf("modbus: connect: %w", err)
	}
	defer handler.Close()

	client := modbus.NewClient(handler)
	var v uint16
	if value {
		v = 0xFF00
	}
	_, err := client.WriteSingleCoil(ctx, addr, v)
	return err
}

// WriteSingleRegister 写单个保持寄存器。
func (w *Writer) WriteSingleRegister(addr, value uint16) error {
	handler := modbus.NewTCPClientHandler(w.address)
	handler.Timeout = w.timeout
	handler.SlaveID = w.slaveID
	ctx := context.Background()
	if err := handler.Connect(ctx); err != nil {
		return fmt.Errorf("modbus: connect: %w", err)
	}
	defer handler.Close()

	client := modbus.NewClient(handler)
	_, err := client.WriteSingleRegister(ctx, addr, value)
	return err
}

// WriteMultipleRegisters 写多个保持寄存器。
func (w *Writer) WriteMultipleRegisters(addr uint16, values []uint16) error {
	handler := modbus.NewTCPClientHandler(w.address)
	handler.Timeout = w.timeout
	handler.SlaveID = w.slaveID
	ctx := context.Background()
	if err := handler.Connect(ctx); err != nil {
		return fmt.Errorf("modbus: connect: %w", err)
	}
	defer handler.Close()

	data := make([]byte, len(values)*2)
	for i, v := range values {
		binary.BigEndian.PutUint16(data[i*2:], v)
	}

	client := modbus.NewClient(handler)
	_, err := client.WriteMultipleRegisters(ctx, addr, uint16(len(values)), data)
	return err
}
