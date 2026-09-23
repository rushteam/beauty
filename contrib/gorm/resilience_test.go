package gorm_test

import (
	"context"
	"errors"
	"testing"
	"time"

	sqlite "github.com/glebarez/sqlite"
	bgorm "github.com/rushteam/beauty/contrib/gorm"
	sqldb "github.com/rushteam/beauty/contrib/sqldb"
)

func TestResilientWrite_CircuitOpens(t *testing.T) {
	db := openMem(t)
	defer db.Close()

	sqlDB, err := db.SQLDB()
	if err != nil {
		t.Fatal(err)
	}

	rw, err := db.ResilientWrite(sqldb.PathResilience{
		Breaker: &sqldb.BreakerSettings{
			Threshold:   0.5,
			MinRequests: 2,
			Window:      time.Minute,
			Cooldown:    time.Minute,
		},
		Pool: sqlDB,
		IsFailure: func(err error) bool {
			return err != nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_ = rw.WithContext(ctx).Exec("UPDATE __nonexistent SET v=1").Error
	}
	err = rw.WithContext(ctx).Exec("UPDATE __nonexistent SET v=1").Error
	if !errors.Is(err, sqldb.ErrCircuitOpen) {
		t.Fatalf("want circuit open, got %v", err)
	}
}

func TestResilientRead_WriteIndependent(t *testing.T) {
	db := openMem(t)
	defer db.Close()
	sqlDB, _ := db.SQLDB()

	write, err := db.ResilientWrite(sqldb.PathResilience{
		Breaker:   &sqldb.BreakerSettings{Threshold: 0.3, MinRequests: 1, Window: time.Minute, Cooldown: time.Minute},
		Pool:      sqlDB,
		IsFailure: func(err error) bool { return err != nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	read, err := db.ResilientRead(sqldb.PathResilience{
		Breaker: &sqldb.BreakerSettings{Threshold: 0.3, MinRequests: 100, Window: time.Minute, Cooldown: time.Minute},
		Pool:    sqlDB,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_ = write.WithContext(ctx).Exec("UPDATE __bad SET v=1").Error
	}
	var u user
	if err := read.WithContext(ctx).First(&u).Error; err != nil && errors.Is(err, sqldb.ErrCircuitOpen) {
		t.Fatalf("read should not trip when write breaker opens: %v", err)
	}
}

func TestResilientWrite_CRUD(t *testing.T) {
	db, err := bgorm.OpenWith(sqlite.Open(":memory:"), nil, bgorm.Config{MaxOpenConns: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.AutoMigrate(&user{}); err != nil {
		t.Fatal(err)
	}

	sqlDB, _ := db.SQLDB()
	rw, err := db.ResilientWrite(sqldb.DefaultWriteResilience(sqlDB))
	if err != nil {
		t.Fatal(err)
	}
	if err := rw.Create(&user{Email: "r@x.com", Name: "bob"}).Error; err != nil {
		t.Fatal(err)
	}
}
