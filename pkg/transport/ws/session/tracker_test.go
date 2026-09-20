package session

import (
	"sync"
	"testing"
	"time"
)

func TestRequestTracker_TrackResolve(t *testing.T) {
	tr := NewRequestTracker()

	id, wait := tr.Track(5 * time.Second)
	if id == 0 {
		t.Fatal("id should be > 0")
	}
	if tr.Pending() != 1 {
		t.Fatalf("pending=%d want 1", tr.Pending())
	}

	// 在另一 goroutine 解决
	go func() {
		time.Sleep(10 * time.Millisecond)
		ok := tr.Resolve(id, []byte("response-data"))
		if !ok {
			t.Error("Resolve should return true")
		}
	}()

	data, err := wait()
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if string(data) != "response-data" {
		t.Fatalf("data=%q", data)
	}
	if tr.Pending() != 0 {
		t.Fatalf("pending=%d want 0", tr.Pending())
	}
}

func TestRequestTracker_Timeout(t *testing.T) {
	tr := NewRequestTracker()
	_, wait := tr.Track(50 * time.Millisecond)

	data, err := wait()
	if err != ErrRequestTimeout {
		t.Fatalf("err=%v want ErrRequestTimeout", err)
	}
	if data != nil {
		t.Fatalf("data should be nil on timeout")
	}
}

func TestRequestTracker_ResolveUnknownID(t *testing.T) {
	tr := NewRequestTracker()
	ok := tr.Resolve(999, []byte("data"))
	if ok {
		t.Fatal("Resolve unknown id should return false")
	}
}

func TestRequestTracker_ResolveZeroID(t *testing.T) {
	tr := NewRequestTracker()
	ok := tr.Resolve(0, []byte("data"))
	if ok {
		t.Fatal("Resolve id=0 should return false")
	}
}

func TestRequestTracker_MultipleRequests(t *testing.T) {
	tr := NewRequestTracker()
	id1, wait1 := tr.Track(5 * time.Second)
	id2, wait2 := tr.Track(5 * time.Second)

	if id1 == id2 {
		t.Fatal("IDs should be unique")
	}
	if tr.Pending() != 2 {
		t.Fatalf("pending=%d want 2", tr.Pending())
	}

	tr.Resolve(id2, []byte("resp2"))
	tr.Resolve(id1, []byte("resp1"))

	data1, _ := wait1()
	data2, _ := wait2()
	if string(data1) != "resp1" || string(data2) != "resp2" {
		t.Fatalf("data1=%q data2=%q", data1, data2)
	}
}

func TestRequestTracker_CancelAll(t *testing.T) {
	tr := NewRequestTracker()
	var wg sync.WaitGroup
	const n = 5
	results := make([][]byte, n)

	for i := 0; i < n; i++ {
		_, wait := tr.Track(10 * time.Second)
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			data, _ := wait()
			results[i] = data
		}()
	}

	time.Sleep(20 * time.Millisecond)
	tr.CancelAll()
	wg.Wait()

	for i, d := range results {
		if d != nil {
			t.Fatalf("results[%d]=%q, want nil (cancelled)", i, d)
		}
	}
	if tr.Pending() != 0 {
		t.Fatalf("pending=%d want 0", tr.Pending())
	}
}

func TestRequestTracker_Interceptor(t *testing.T) {
	tr := NewRequestTracker()

	extractID := func(data []byte) uint64 {
		// 简单协议:前 8 字节 = uint64 big-endian ID
		if len(data) < 8 {
			return 0
		}
		var id uint64
		for i := 0; i < 8; i++ {
			id = id<<8 | uint64(data[i])
		}
		return id
	}

	interceptor := tr.Interceptor(extractID)

	id, wait := tr.Track(5 * time.Second)

	// 模拟收到响应:前 8 字节是 id,后面是 payload
	resp := make([]byte, 8+5)
	for i := 7; i >= 0; i-- {
		resp[7-i] = byte(id >> (i * 8))
	}
	copy(resp[8:], "hello")

	consume, err := interceptor(nil, 0, resp)
	if err != nil {
		t.Fatalf("interceptor err: %v", err)
	}
	if !consume {
		t.Fatal("interceptor should consume matching response")
	}

	data, err := wait()
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if string(data[8:]) != "hello" {
		t.Fatalf("data=%q", data)
	}

	// 非匹配消息不消费
	consume, _ = interceptor(nil, 0, []byte("short"))
	if consume {
		t.Fatal("short data should not be consumed")
	}
}
