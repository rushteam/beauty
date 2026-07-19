// sqlc + contrib/sqldb 端到端样例:读写分离下用 sqlc 生成的类型安全查询。
//
// 为可运行(不依赖真实数据库),主库与只读副本都指向**同一个 sqlite 文件**——真实部署里
// 副本是独立的复制从库(sqldb.Config.ReplicaDSNs 填从库地址即可,代码不变)。
//
// 运行:
//
//	cd contrib/sqldb/example && go run .
//
// 重新生成 appdb(改了 SQL 后):装 sqlc 后在本目录执行 `sqlc generate`(见 sqlc.yaml)。
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // 纯 Go sqlite 驱动(免 cgo),注册驱动名 "sqlite"

	"github.com/rushteam/beauty/contrib/sqldb"
	"github.com/rushteam/beauty/contrib/sqldb/example/appdb"
)

const schema = `CREATE TABLE IF NOT EXISTS authors (
	id   INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL,
	bio  TEXT
);`

func main() {
	path := filepath.Join(os.TempDir(), "beauty_sqlc_demo.db")
	_ = os.Remove(path) // 每次跑用全新库
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)"

	db, err := sqldb.Open(sqldb.Config{
		Driver:      "sqlite",
		PrimaryDSN:  dsn,
		ReplicaDSNs: []string{dsn}, // 真实部署填独立从库;这里同库以便可运行
		// demo 无 telemetry,关掉埋点更干净(生产默认开)
		DisableTracing: true,
		DisableMetrics: true,
	})
	if err != nil {
		log.Fatalf("open: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	if _, err := db.Primary().ExecContext(ctx, schema); err != nil { // 迁移在主库上做
		log.Fatalf("migrate: %v", err)
	}

	write := appdb.New(db.Writer()) // 写走主库
	read := appdb.New(db.Reader())  // 读走副本
	rw := appdb.New(db.RW())        // 自动路由(按 SQL 意图)

	// 1) 显式写:CreateAuthor 是 :one + RETURNING → 走 QueryRow;用 Writer() 落主库。
	alice, err := write.CreateAuthor(ctx, appdb.CreateAuthorParams{
		Name: "Alice", Bio: sql.NullString{String: "poet", Valid: true},
	})
	if err != nil {
		log.Fatalf("create alice: %v", err)
	}
	fmt.Printf("写(Writer):创建 #%d %s\n", alice.ID, alice.Name)

	// 2) 自动路由:同样是 RETURNING(QueryRow 却是写),RW() 嗅探到写意图,照样正确落主库。
	bob, err := rw.CreateAuthor(ctx, appdb.CreateAuthorParams{Name: "Bob"})
	if err != nil {
		log.Fatalf("create bob via RW: %v", err)
	}
	fmt.Printf("写(RW 自动识别 RETURNING→主库):创建 #%d %s\n", bob.ID, bob.Name)

	// 3) 读:ListAuthors 是纯 SELECT → 走副本(此处同库,故看得到刚写入的)。
	authors, err := read.ListAuthors(ctx)
	if err != nil {
		log.Fatalf("list: %v", err)
	}
	fmt.Printf("读(Reader,共 %d 位):\n", len(authors))
	for _, a := range authors {
		fmt.Printf("  #%d %-6s bio=%q\n", a.ID, a.Name, a.Bio.String)
	}

	// 4) 读己之写/强一致:需要立刻读到主库最新时,用 Primary(ctx) 强制走主库。
	got, err := rw.GetAuthor(sqldb.Primary(ctx), alice.ID)
	if err != nil {
		log.Fatalf("get: %v", err)
	}
	fmt.Printf("读己之写(RW + Primary(ctx)→主库):#%d %s\n", got.ID, got.Name)
}
