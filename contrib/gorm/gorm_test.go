package gorm_test

import (
	"context"
	"errors"
	"testing"

	sqlite "github.com/glebarez/sqlite"
	gosqldriver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"

	bgorm "github.com/rushteam/beauty/contrib/gorm"
)

type user struct {
	ID    uint   `gorm:"primaryKey"`
	Email string `gorm:"uniqueIndex"`
	Name  string
}

func openMem(t *testing.T) *bgorm.DB {
	t.Helper()
	// glebarez/sqlite 纯 Go(免 cgo);MaxOpenConns=1 让 :memory: 只用一条连接,
	// 迁移与后续读写落在同一内存库。
	db, err := bgorm.OpenWith(sqlite.Open(":memory:"), nil, bgorm.Config{MaxOpenConns: 1})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&user{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// TestOpenAndCRUD:端到端建连接 + 迁移 + 增查 + Write/Read 句柄 + Ping/Close。
func TestOpenAndCRUD(t *testing.T) {
	db := openMem(t)
	defer db.Close()

	if err := db.Create(&user{Email: "a@x.com", Name: "alice"}).Error; err != nil {
		t.Fatalf("create: %v", err)
	}

	var got user
	if err := db.Read().First(&got, "email = ?", "a@x.com").Error; err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Name != "alice" {
		t.Fatalf("name = %q, want alice", got.Name)
	}

	// 无副本时 Write/Read 应返回可用句柄(等价于主库)。
	if db.Write() == nil || db.Read() == nil {
		t.Fatal("Write/Read 句柄不应为 nil")
	}
	if err := db.Ping(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

// TestDuplicate:唯一键冲突返回错误(方言是否翻译因驱动而异,这里只断言"有错")。
func TestDuplicate(t *testing.T) {
	db := openMem(t)
	defer db.Close()
	if err := db.Create(&user{Email: "dup@x.com"}).Error; err != nil {
		t.Fatalf("first create: %v", err)
	}
	err := db.Create(&user{Email: "dup@x.com"}).Error
	if err == nil {
		t.Fatal("重复唯一键应报错")
	}
}

// TestIsDuplicatedKey:错误判定逻辑(合成错误,不依赖具体方言翻译,确定性)。
func TestIsDuplicatedKey(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"gorm-dup", gorm.ErrDuplicatedKey, true},
		{"wrapped-gorm-dup", errors.Join(errors.New("ctx"), gorm.ErrDuplicatedKey), true},
		{"mysql-1062", &gosqldriver.MySQLError{Number: 1062, Message: "dup"}, true},
		{"mysql-other", &gosqldriver.MySQLError{Number: 1045}, false},
		{"random", errors.New("boom"), false},
	}
	for _, c := range cases {
		if got := bgorm.IsDuplicatedKey(c.err); got != c.want {
			t.Errorf("%s: IsDuplicatedKey = %v, want %v", c.name, got, c.want)
		}
	}
}
