// outbox demo:事务性发件箱(transactional outbox)——"改库 + 发消息"的原子性,用现成的
// database/sql 事务拼出来,不需要额外框架/模块。
//
// 核心思想:在**业务事务的同一个 tx 里**顺带 INSERT 一行到 outbox 表(同事务 → 原子),
// 之后由 relay 轮询发件箱、投给消息系统、投成功即删。进程在任何点崩溃都不会出现
// "库改了但消息没发"或"消息发了但库回滚"。at-least-once:消费端需幂等。
//
// 这是**应用层模式**,不是库——下面 40 行 enqueue/relay 就是全部实现,照抄到你项目里改即可。
// 生产化建议见文末 README 与本文注释。
//
// 运行:go run .(纯内存 sqlite,自包含)
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"

	_ "modernc.org/sqlite" // 纯 Go sqlite 驱动(免 cgo)
)

// Message 是要发出去的消息(与具体 broker 无关)。
type Message struct {
	Topic   string
	Key     string
	Payload []byte
	Headers map[string]string
}

// Publisher 是发件箱的投递目标。demo 用打印实现;生产替换成 pkg/mq / contrib/nats 等:
//
//	type mqPub struct{ p mq.Publisher }
//	func (a mqPub) Publish(ctx context.Context, m Message) error {
//	    return a.p.Publish(ctx, mq.Message{Topic: m.Topic, Key: m.Key, Body: m.Payload, Headers: m.Headers})
//	}
type Publisher interface {
	Publish(ctx context.Context, m Message) error
}

// enqueue 把消息写进发件箱——**用调用方的 tx**,与业务写同一事务,原子。
// tx 是 *sql.Tx(或任何有 ExecContext 的东西:gorm tx / sqlx.Tx / sqldb.Writer)。
func enqueue(ctx context.Context, tx *sql.Tx, m Message) error {
	hdr, _ := json.Marshal(m.Headers)
	_, err := tx.ExecContext(ctx,
		`INSERT INTO outbox(topic, msg_key, payload, headers) VALUES(?,?,?,?)`,
		m.Topic, m.Key, m.Payload, hdr)
	return err
}

// relayOnce 拉一批未发消息,逐条投递,投成功即删;返回本次投出的条数。
// 投递失败即停(剩余留到下轮重投)。生产里放到后台循环 + **只在 leader 上跑**(用 dlock/cron 选主),
// 单实例就无需 SELECT ... FOR UPDATE SKIP LOCKED,SQL 简单、方言通用。
func relayOnce(ctx context.Context, db *sql.DB, pub Publisher, batch int) (int, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, topic, msg_key, payload, headers FROM outbox ORDER BY id LIMIT ?`, batch)
	if err != nil {
		return 0, err
	}
	type item struct {
		id  int64
		msg Message
	}
	var items []item
	for rows.Next() {
		var it item
		var hdr []byte
		if err := rows.Scan(&it.id, &it.msg.Topic, &it.msg.Key, &it.msg.Payload, &hdr); err != nil {
			rows.Close()
			return 0, err
		}
		_ = json.Unmarshal(hdr, &it.msg.Headers)
		items = append(items, it)
	}
	rows.Close()

	sent := 0
	for _, it := range items {
		if err := pub.Publish(ctx, it.msg); err != nil {
			return sent, err // 失败即停,下轮重投(at-least-once)
		}
		if _, err := db.ExecContext(ctx, `DELETE FROM outbox WHERE id=?`, it.id); err != nil {
			return sent, err
		}
		sent++
	}
	return sent, nil
}

// ---- 下面是把上面两个函数串起来的演示 ----

type logPublisher struct{}

func (logPublisher) Publish(_ context.Context, m Message) error {
	fmt.Printf("  📤 发布 topic=%s key=%s payload=%s\n", m.Topic, m.Key, m.Payload)
	return nil
}

func main() {
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1) // :memory: 单连接,保证数据可见

	mustExec(ctx, db, `CREATE TABLE accounts (id TEXT PRIMARY KEY, balance INT)`)
	mustExec(ctx, db, `CREATE TABLE outbox (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		topic TEXT NOT NULL, msg_key TEXT, payload BLOB NOT NULL, headers TEXT,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP)`)
	mustExec(ctx, db, `INSERT INTO accounts VALUES ('alice', 100), ('bob', 0)`)

	// 1) 原子:转账 + 发件箱,同一事务提交。
	fmt.Println("① 转账 30(改库 + 入发件箱,同一事务):")
	tx, _ := db.BeginTx(ctx, nil)
	mustTx(ctx, tx, `UPDATE accounts SET balance=balance-30 WHERE id='alice'`)
	mustTx(ctx, tx, `UPDATE accounts SET balance=balance+30 WHERE id='bob'`)
	if err := enqueue(ctx, tx, Message{Topic: "transfer", Key: "t1", Payload: []byte(`{"from":"alice","to":"bob","amt":30}`)}); err != nil {
		log.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("   提交后:发件箱 %d 条,余额 %v\n", count(ctx, db, "outbox"), balances(ctx, db))

	// 2) 原子性反证:事务中途失败 → 回滚 → 发件箱不留痕、业务不变。
	fmt.Println("② 转账失败(回滚):库不变,发件箱也不会多出消息:")
	tx2, _ := db.BeginTx(ctx, nil)
	mustTx(ctx, tx2, `UPDATE accounts SET balance=balance-999 WHERE id='alice'`)
	_ = enqueueMust(ctx, tx2, Message{Topic: "transfer", Key: "t2", Payload: []byte(`{"amt":999}`)})
	_ = tx2.Rollback() // 模拟中途出错回滚(发件箱那条 INSERT 也随之丢弃)
	fmt.Printf("   回滚后:发件箱仍 %d 条,余额 %v\n", count(ctx, db, "outbox"), balances(ctx, db))

	// 3) relay 把发件箱投出去,投成功即删。
	fmt.Println("③ relay 投递发件箱:")
	n, err := relayOnce(ctx, db, logPublisher{}, 100)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("   投出 %d 条;剩余发件箱 %d 条\n", n, count(ctx, db, "outbox"))
}

// ---- demo 辅助 ----

func mustExec(ctx context.Context, db *sql.DB, q string) {
	if _, err := db.ExecContext(ctx, q); err != nil {
		log.Fatalf("exec %q: %v", q, err)
	}
}
func mustTx(ctx context.Context, tx *sql.Tx, q string) {
	if _, err := tx.ExecContext(ctx, q); err != nil {
		log.Fatalf("tx exec %q: %v", q, err)
	}
}
func enqueueMust(ctx context.Context, tx *sql.Tx, m Message) error {
	if err := enqueue(ctx, tx, m); err != nil {
		log.Fatal(err)
	}
	return nil
}
func count(ctx context.Context, db *sql.DB, table string) int {
	var n int
	_ = db.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&n)
	return n
}
func balances(ctx context.Context, db *sql.DB) map[string]int {
	m := map[string]int{}
	rows, _ := db.QueryContext(ctx, `SELECT id, balance FROM accounts ORDER BY id`)
	defer rows.Close()
	for rows.Next() {
		var id string
		var b int
		_ = rows.Scan(&id, &b)
		m[id] = b
	}
	return m
}
