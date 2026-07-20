// wallet 示例:虚拟货币账本,差额更新 + 不可超扣。
//
// 演示 pkg/wallet:Apply 入账/扣账(余额不足拒绝)、Ledgers 查账本、Balance 查余额。
// HTTP 端点:/earn /spend /balance /ledgers。
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/domain/wallet"
	"github.com/rushteam/beauty/pkg/service/webserver"
)

func main() {
	w := wallet.New()
	var seq atomic.Int64

	earn := func(user, cur string, amt int64) {
		w.Apply(user, wallet.WalletMap{cur: amt}, "earn", time.Now().UnixNano())
	}

	mux := http.NewServeMux()

	// /earn?user=u1&cur=gold&amt=100  入账。
	mux.HandleFunc("/earn", func(rw http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		amt, _ := strconv.ParseInt(q.Get("amt"), 10, 64)
		_, l, err := w.Apply(q.Get("user"), wallet.WalletMap{q.Get("cur"): amt}, "earn", time.Now().UnixNano())
		if err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		json.NewEncoder(rw).Encode(map[string]any{"user": q.Get("user"), "ledger_id": l.ID})
	})

	// /spend?user=u1&cur=gold&amt=30  扣账(余额不足拒绝)。
	mux.HandleFunc("/spend", func(rw http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		amt, _ := strconv.ParseInt(q.Get("amt"), 10, 64)
		bal, _, err := w.Apply(q.Get("user"), wallet.WalletMap{q.Get("cur"): -amt}, "spend #"+strconv.FormatInt(seq.Add(1), 10), time.Now().UnixNano())
		if err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		json.NewEncoder(rw).Encode(map[string]any{"user": q.Get("user"), "new_balance": bal})
	})

	// /balance?user=u1  查余额。
	mux.HandleFunc("/balance", func(rw http.ResponseWriter, r *http.Request) {
		json.NewEncoder(rw).Encode(w.Balance(r.URL.Query().Get("user")))
	})

	// /ledgers?user=u1  查账本。
	mux.HandleFunc("/ledgers", func(rw http.ResponseWriter, r *http.Request) {
		json.NewEncoder(rw).Encode(w.Ledgers(r.URL.Query().Get("user")))
	})

	_ = earn
	app := beauty.New(beauty.WithWebServer(":8288", mux, webserver.WithServiceName("wallet-demo")))
	println("wallet demo on :8288")
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}
