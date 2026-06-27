package storage_test

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/domain/storage"
)

func ver(v []byte) string {
	sum := md5.Sum(v)
	return hex.EncodeToString(sum[:])
}

func TestStorage_WriteIfNotExist(t *testing.T) {
	s := storage.New()
	o, err := s.Write(storage.WriteOp{
		OwnerID: "u1", Collection: "save", Key: "slot1",
		Value: []byte("hello"), Mode: storage.WriteIfNotExist,
		ReadAccess: 0, WriteAccess: 1,
	}, 1)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if o.Version == "" {
		t.Fatal("version empty")
	}
	// 再次 IfNotExist 应冲突。
	if _, err := s.Write(storage.WriteOp{
		OwnerID: "u1", Collection: "save", Key: "slot1",
		Value: []byte("hello2"), Mode: storage.WriteIfNotExist,
	}, 2); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
}

func TestStorage_WriteIfMatch(t *testing.T) {
	s := storage.New()
	o, _ := s.Write(storage.WriteOp{
		OwnerID: "u1", Collection: "c", Key: "k",
		Value: []byte("v1"), Mode: storage.WriteIfNotExist,
		WriteAccess: 1,
	}, 1)
	// 正确 version → 成功。
	o2, err := s.Write(storage.WriteOp{
		OwnerID: "u1", Collection: "c", Key: "k",
		Value: []byte("v2"), Mode: storage.WriteIfMatch, Version: o.Version,
		WriteAccess: 1,
	}, 2)
	if err != nil {
		t.Fatalf("ifmatch: %v", err)
	}
	if o2.Version == o.Version {
		t.Fatal("version should change after update")
	}
	// 旧 version → 冲突。
	if _, err := s.Write(storage.WriteOp{
		OwnerID: "u1", Collection: "c", Key: "k",
		Value: []byte("v3"), Mode: storage.WriteIfMatch, Version: o.Version,
		WriteAccess: 1,
	}, 3); !errors.Is(err, storage.ErrVersionMismatch) {
		t.Fatalf("want ErrVersionMismatch, got %v", err)
	}
}

func TestStorage_WriteLastWriteWins(t *testing.T) {
	s := storage.New()
	s.Write(storage.WriteOp{OwnerID: "u1", Collection: "c", Key: "k", Value: []byte("v1"), Mode: storage.WriteIfNotExist, WriteAccess: 1}, 1)
	o, err := s.Write(storage.WriteOp{OwnerID: "u1", Collection: "c", Key: "k", Value: []byte("v2"), Mode: storage.WriteLastWriteWins, WriteAccess: 1}, 2)
	if err != nil {
		t.Fatalf("lww: %v", err)
	}
	if string(o.Value) != "v2" {
		t.Fatalf("value=%s", o.Value)
	}
}

func TestStorage_ReadPermission(t *testing.T) {
	s := storage.New()
	s.Write(storage.WriteOp{OwnerID: "u1", Collection: "c", Key: "k", Value: []byte("secret"), Mode: storage.WriteIfNotExist, ReadAccess: 0, WriteAccess: 1}, 1)
	// owner 可读。
	o, err := s.Read("u1", "c", "k", "u1")
	if err != nil || o == nil {
		t.Fatalf("owner read: %v %v", o, err)
	}
	// 他人不可读。
	if _, err := s.Read("u1", "c", "k", "u2"); err == nil {
		t.Fatal("u2 should not read private")
	}
	// 公开对象:他人可读。
	s.Write(storage.WriteOp{OwnerID: "u1", Collection: "c", Key: "pub", Value: []byte("p"), Mode: storage.WriteIfNotExist, ReadAccess: 2, WriteAccess: 1}, 2)
	o, err = s.Read("u1", "c", "pub", "u2")
	if err != nil || o == nil {
		t.Fatalf("public read: %v %v", o, err)
	}
}

func TestStorage_ReadOnlyObject(t *testing.T) {
	s := storage.New()
	s.Write(storage.WriteOp{OwnerID: "u1", Collection: "c", Key: "k", Value: []byte("v"), Mode: storage.WriteIfNotExist, WriteAccess: 0}, 1)
	// 只读对象不可写。
	if _, err := s.Write(storage.WriteOp{OwnerID: "u1", Collection: "c", Key: "k", Value: []byte("v2"), Mode: storage.WriteLastWriteWins, WriteAccess: 0}, 2); !errors.Is(err, storage.ErrReadOnly) {
		t.Fatalf("want ErrReadOnly, got %v", err)
	}
	// 不可删。
	if err := s.Delete("u1", "c", "k", "u1"); !errors.Is(err, storage.ErrReadOnly) {
		t.Fatalf("delete want ErrReadOnly, got %v", err)
	}
}

func TestStorage_WriteBatchAtomic(t *testing.T) {
	s := storage.New()
	ops := []storage.WriteOp{
		{OwnerID: "u1", Collection: "c", Key: "k1", Value: []byte("v1"), Mode: storage.WriteIfNotExist, WriteAccess: 1},
		{OwnerID: "u1", Collection: "c", Key: "k2", Value: []byte("v2"), Mode: storage.WriteIfNotExist, WriteAccess: 1},
	}
	res, err := s.WriteBatch(ops, 1)
	if err != nil || len(res) != 2 {
		t.Fatalf("batch: %v len=%d", err, len(res))
	}
	if s.Count() != 2 {
		t.Fatalf("count=%d", s.Count())
	}
	// 第三个 op 与 k1 冲突 → 整体回滚(k3 不应存在)。
	ops2 := []storage.WriteOp{
		{OwnerID: "u1", Collection: "c", Key: "k3", Value: []byte("v3"), Mode: storage.WriteIfNotExist, WriteAccess: 1},
		{OwnerID: "u1", Collection: "c", Key: "k1", Value: []byte("v1x"), Mode: storage.WriteIfNotExist, WriteAccess: 1}, // 冲突
	}
	if _, err := s.WriteBatch(ops2, 2); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
	// k3 不应存在(回滚)。
	if o, _ := s.Read("u1", "c", "k3", "u1"); o != nil {
		t.Fatal("k3 should not exist after rollback")
	}
}

func TestStorage_List(t *testing.T) {
	s := storage.New()
	s.Write(storage.WriteOp{OwnerID: "u1", Collection: "c", Key: "k1", Value: []byte("v1"), Mode: storage.WriteIfNotExist, ReadAccess: 2, WriteAccess: 1}, 1)
	s.Write(storage.WriteOp{OwnerID: "u1", Collection: "c", Key: "k2", Value: []byte("v2"), Mode: storage.WriteIfNotExist, ReadAccess: 2, WriteAccess: 1}, 2)
	s.Write(storage.WriteOp{OwnerID: "u1", Collection: "other", Key: "k3", Value: []byte("v3"), Mode: storage.WriteIfNotExist, ReadAccess: 2, WriteAccess: 1}, 3)
	list := s.List("c", "u2")
	if len(list) != 2 {
		t.Fatalf("list len=%d", len(list))
	}
}

func TestStorage_Delete(t *testing.T) {
	s := storage.New()
	s.Write(storage.WriteOp{OwnerID: "u1", Collection: "c", Key: "k", Value: []byte("v"), Mode: storage.WriteIfNotExist, WriteAccess: 1}, 1)
	if err := s.Delete("u1", "c", "k", "u1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if o, _ := s.Read("u1", "c", "k", "u1"); o != nil {
		t.Fatal("should be deleted")
	}
	if s.Count() != 0 {
		t.Fatal("count != 0")
	}
}

func TestStorage_Eviction(t *testing.T) {
	s := storage.New(storage.WithMaxEntries(5))
	for i := 0; i < 20; i++ {
		s.Write(storage.WriteOp{OwnerID: "u1", Collection: "c", Key: "k" + itoa(i), Value: []byte("v"), Mode: storage.WriteLastWriteWins, WriteAccess: 1}, int64(i+1))
	}
	// 阈值 5*1.1=5.5→5,但 evictLocked 在 >5 时触发,删到 5。
	if s.Count() > 5 {
		t.Fatalf("count=%d, should be <=5 after eviction", s.Count())
	}
}

func TestStorage_VersionIsMD5(t *testing.T) {
	s := storage.New()
	o, _ := s.Write(storage.WriteOp{OwnerID: "u1", Collection: "c", Key: "k", Value: []byte("hello"), Mode: storage.WriteIfNotExist, WriteAccess: 1}, 1)
	if o.Version != ver([]byte("hello")) {
		t.Fatalf("version=%s, want %s", o.Version, ver([]byte("hello")))
	}
}

func TestStorage_ConcurrentWriteIfMatch(t *testing.T) {
	s := storage.New()
	o, _ := s.Write(storage.WriteOp{OwnerID: "u1", Collection: "c", Key: "k", Value: []byte("v0"), Mode: storage.WriteIfNotExist, WriteAccess: 1}, 1)
	var wg sync.WaitGroup
	var ok, fail int64
	var mu sync.Mutex
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.Write(storage.WriteOp{
				OwnerID: "u1", Collection: "c", Key: "k",
				Value: []byte("v"), Mode: storage.WriteIfMatch, Version: o.Version,
				WriteAccess: 1,
			}, time.Now().UnixNano())
			mu.Lock()
			if err == nil {
				ok++
			} else {
				fail++
			}
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	// 只有一个 IfMatch 能成功(用初始 version),其余冲突。
	if ok != 1 {
		t.Fatalf("ok=%d, want 1", ok)
	}
	if fail != 49 {
		t.Fatalf("fail=%d, want 49", fail)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
