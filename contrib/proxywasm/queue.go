package proxywasm

import "sync"

// SharedQueue 实现 Proxy-Wasm 的跨实例消息队列语义。
// 一个队列由 (vm_id, name) 唯一标识, 支持 register/resolve/enqueue/dequeue。
// 当有消息入队时, 注册方通过 proxy_on_queue_ready 回调获得通知。
type SharedQueue struct {
	mu     sync.Mutex
	queues map[uint32]*queueEntry
	byName map[string]uint32 // "vm_id\x00name" → queue_id
	nextID uint32
}

type queueEntry struct {
	vmID string
	name string
	data [][]byte
}

func newSharedQueue() *SharedQueue {
	return &SharedQueue{
		queues: make(map[uint32]*queueEntry),
		byName: make(map[string]uint32),
		nextID: 1,
	}
}

// Register 注册一个队列, 返回队列 ID。如已存在则返回现有 ID。
func (sq *SharedQueue) Register(vmID, name string) uint32 {
	sq.mu.Lock()
	defer sq.mu.Unlock()
	key := vmID + "\x00" + name
	if id, ok := sq.byName[key]; ok {
		return id
	}
	id := sq.nextID
	sq.nextID++
	sq.queues[id] = &queueEntry{vmID: vmID, name: name}
	sq.byName[key] = id
	return id
}

// Resolve 根据 vm_id 和 name 查找队列 ID。
func (sq *SharedQueue) Resolve(vmID, name string) (uint32, bool) {
	sq.mu.Lock()
	defer sq.mu.Unlock()
	key := vmID + "\x00" + name
	id, ok := sq.byName[key]
	return id, ok
}

// Enqueue 向指定队列追加一条消息。
func (sq *SharedQueue) Enqueue(queueID uint32, data []byte) bool {
	sq.mu.Lock()
	defer sq.mu.Unlock()
	q, ok := sq.queues[queueID]
	if !ok {
		return false
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	q.data = append(q.data, cp)
	return true
}

// Dequeue 从指定队列取出一条消息(FIFO)。
func (sq *SharedQueue) Dequeue(queueID uint32) ([]byte, bool) {
	sq.mu.Lock()
	defer sq.mu.Unlock()
	q, ok := sq.queues[queueID]
	if !ok || len(q.data) == 0 {
		return nil, false
	}
	msg := q.data[0]
	q.data = q.data[1:]
	return msg, true
}

// Len 返回队列中待消费的消息数。
func (sq *SharedQueue) Len(queueID uint32) int {
	sq.mu.Lock()
	defer sq.mu.Unlock()
	q, ok := sq.queues[queueID]
	if !ok {
		return 0
	}
	return len(q.data)
}
