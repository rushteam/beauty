// clan 示例:用现有原语组合出"公会"语义,不新增包。
//
// 证明 pkg/ 的原语组合已足够覆盖公会场景:
//   - pkg/domain/relationship:成员与角色(leader/member),加入/退出/踢人;
//   - pkg/domain/tournament:公会战赛季榜(cron 重置 + 排名);
//   - pkg/domain/wallet:公会基金(捐赠/发放);
//   - pkg/domain/party:公会内小队(临时组队开黑)。
//
// 路由:
//   /create?clan=c1&leader=alice           创建公会(relationship 加 leader 边)
//   /join?clan=c1&user=bob                 加入公会(加 member 边)
//   /members?clan=c1                       成员列表(按角色过滤)
//   /donate?clan=c1&user=alice&amount=100  捐赠公会基金
//   /fund?clan=c1                          查公会基金
//   /score?clan=c1&user=alice&score=100    公会战赛季榜提交
//   /ranking?clan=c1                       公会战排名
//   /squad?clan=c1&leader=alice            公会内组小队(party)
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/domain/relationship"
	"github.com/rushteam/beauty/pkg/domain/tournament"
	"github.com/rushteam/beauty/pkg/domain/wallet"
	"github.com/rushteam/beauty/pkg/leaderboard"
	"github.com/rushteam/beauty/pkg/service/webserver"
)

// Clan 用现有原语组合出的公会聚合体。
type Clan struct {
	graph *relationship.Graph   // 成员与角色
	fund  *wallet.Wallet        // 公会基金
	war   *tournament.Tournament // 公会战赛季榜
}

var clans = map[string]*Clan{}

func getClan(id string) *Clan {
	c, ok := clans[id]
	if !ok {
		c = &Clan{
			graph: relationship.New(),
			fund:  wallet.New(),
		}
		// 公会战赛季榜:每日重置,降序(分高在前)。
		t, _ := tournament.New("clanwar-"+id, leaderboard.SortDescending, "0 0 * * *")
		c.war = t
		clans[id] = c
	}
	return c
}

func main() {
	mux := http.NewServeMux()

	// /create?clan=c1&leader=alice  创建公会。
	mux.HandleFunc("/create", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		clanID, leader := q.Get("clan"), q.Get("leader")
		if _, ok := clans[clanID]; ok {
			http.Error(w, "clan exists", http.StatusBadRequest)
			return
		}
		c := getClan(clanID)
		// leader 用 StateOwner 角色(自己→自己建立 owner 边,标识公会归属)。
		_ = c.graph.AddEdge(relationship.Edge{
			Source: clanID, Destination: leader, State: relationship.StateOwner, Position: time.Now().UnixNano(),
		})
		// leader 同时是公会成员。
		_ = c.graph.AddEdge(relationship.Edge{
			Source: clanID, Destination: leader, State: relationship.StateActive, Position: time.Now().UnixNano() + 1,
		})
		// 初始化公会基金账户。
		c.fund.SetBalance(clanID, wallet.WalletMap{"gold": 0})
		w.Write([]byte("created"))
	})

	// /join?clan=c1&user=bob  加入公会(加 active 成员边)。
	mux.HandleFunc("/join", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		clanID := q.Get("clan")
		c := getClan(clanID)
		user := q.Get("user")
		if err := c.graph.AddEdge(relationship.Edge{
			Source: clanID, Destination: user, State: relationship.StateActive, Position: time.Now().UnixNano(),
		}); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Write([]byte("joined"))
	})

	// /members?clan=c1  成员列表(按角色:owner 在前,member 在后)。
	mux.HandleFunc("/members", func(w http.ResponseWriter, r *http.Request) {
		clanID := r.URL.Query().Get("clan")
		c := getClan(clanID)
		// 查公会指向的所有 active 边(成员)。
		edges := c.graph.Outgoing(clanID, 0, 100, relationship.StateActive)
		owners := c.graph.Outgoing(clanID, 0, 100, relationship.StateOwner)
		out := map[string][]string{
			"leader":  toUsers(owners),
			"members": toUsers(edges),
		}
		json.NewEncoder(w).Encode(out)
	})

	// /donate?clan=c1&user=alice&amount=100  捐赠基金。
	mux.HandleFunc("/donate", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		c := getClan(q.Get("clan"))
		amount, _ := strconv.ParseInt(q.Get("amount"), 10, 64)
		_, _, err := c.fund.Apply(q.Get("clan"), wallet.WalletMap{"gold": amount}, "donate:"+q.Get("user"), time.Now().UnixNano())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Write([]byte("donated " + strconv.FormatInt(amount, 10)))
	})

	// /fund?clan=c1  查公会基金。
	mux.HandleFunc("/fund", func(w http.ResponseWriter, r *http.Request) {
		c := getClan(r.URL.Query().Get("clan"))
		json.NewEncoder(w).Encode(c.fund.Balance(r.URL.Query().Get("clan")))
	})

	// /score?clan=c1&user=alice&score=100  公会战赛季榜提交。
	mux.HandleFunc("/score", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		c := getClan(q.Get("clan"))
		score, _ := strconv.ParseInt(q.Get("score"), 10, 64)
		c.war.Insert(leaderboard.Record{OwnerID: q.Get("user"), Score: score}, true)
		w.Write([]byte("submitted"))
	})

	// /ranking?clan=c1  公会战 Top 10。
	mux.HandleFunc("/ranking", func(w http.ResponseWriter, r *http.Request) {
		c := getClan(r.URL.Query().Get("clan"))
		json.NewEncoder(w).Encode(c.war.TopN(10))
	})

	app := beauty.New(beauty.WithWebServer(":8301", mux, webserver.WithServiceName("clan-demo")))
	println("clan demo on :8301")
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}

func toUsers(edges []relationship.Edge) []string {
	out := make([]string, 0, len(edges))
	for _, e := range edges {
		out = append(out, e.Destination)
	}
	return out
}
