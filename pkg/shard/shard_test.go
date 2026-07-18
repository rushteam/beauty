package shard_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rushteam/beauty/pkg/shard"
)

func mem(id, addr string) shard.Member { return shard.StaticMember{NodeID: id, NodeAddr: addr} }

// TestSharder_OwnerStableAndUnique:归属确定且稳定;同一 key 在三个实例视角下恰有一个 IsLocal。
func TestSharder_OwnerStableAndUnique(t *testing.T) {
	members := []shard.Member{mem("a", "a:1"), mem("b", "b:1"), mem("c", "c:1")}
	sa := shard.New("a", members)
	sb := shard.New("b", members)
	sc := shard.New("c", members)

	for i := range 200 {
		key := fmt.Sprintf("stream-%d", i)
		owner, ok := sa.Owner(key)
		if !ok {
			t.Fatalf("key %q 无归属", key)
		}
		// 三个实例对同一 key 的归属一致。
		if o2, _ := sb.Owner(key); o2.ID() != owner.ID() {
			t.Fatalf("key %q 归属不一致: a 看到 %s, b 看到 %s", key, owner.ID(), o2.ID())
		}
		// 恰好一个实例认为本地。
		local := 0
		for _, s := range []*shard.Sharder{sa, sb, sc} {
			if s.IsLocal(key) {
				local++
			}
		}
		if local != 1 {
			t.Fatalf("key %q 有 %d 个实例认为本地, want 1", key, local)
		}
	}
	// 稳定性:重复查询同一结果。
	o1, _ := sa.Owner("stream-42")
	o2, _ := sa.Owner("stream-42")
	if o1.ID() != o2.ID() {
		t.Fatal("同 key 归属应稳定")
	}
}

// TestSharder_EmptyIsLocal:无成员(单机 / 尚未发现)时一切视为本地。
func TestSharder_EmptyIsLocal(t *testing.T) {
	s := shard.New("solo", nil)
	if !s.IsLocal("anything") {
		t.Fatal("空成员集应视为本地")
	}
	if _, ok := s.Owner("anything"); ok {
		t.Fatal("空成员集不应有归属")
	}
}

// TestRouter_ProxiesToOwner:非本地 key 反代给归属实例,本地 key 走本地 handler。
func TestRouter_ProxiesToOwner(t *testing.T) {
	// 后端 b:代表另一台实例,回显自己的身份。
	backendB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "REMOTE-B:%s", r.URL.Path)
	}))
	defer backendB.Close()

	members := []shard.Member{
		mem("a", "http://127.0.0.1:1"), // self;本地不经网络
		mem("b", backendB.URL),
	}
	sh := shard.New("a", members)

	// 找一个归属 b 的 key 和一个归属 a 的 key。
	var keyB, keyA string
	for i := range 1000 {
		k := fmt.Sprintf("k%d", i)
		if o, _ := sh.Owner(k); o.ID() == "b" && keyB == "" {
			keyB = k
		} else if o.ID() == "a" && keyA == "" {
			keyA = k
		}
		if keyA != "" && keyB != "" {
			break
		}
	}
	if keyA == "" || keyB == "" {
		t.Fatalf("未找到分属 a/b 的 key(keyA=%q keyB=%q)", keyA, keyB)
	}

	local := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "LOCAL-A")
	})
	rt := shard.NewRouter(sh, shard.PathHeadKey, local)
	front := httptest.NewServer(rt)
	defer front.Close()

	// 归属 a 的 key → 本地 handler。
	if body := get(t, front.URL+"/"+keyA+"/index.m3u8"); body != "LOCAL-A" {
		t.Fatalf("本地 key 应走 local, got %q", body)
	}
	// 归属 b 的 key → 反代到 backendB。
	if body := get(t, front.URL+"/"+keyB+"/index.m3u8"); body != "REMOTE-B:/"+keyB+"/index.m3u8" {
		t.Fatalf("非本地 key 应反代到 b, got %q", body)
	}
}

// TestRouter_LoopGuard:带"已代理"标记且仍非本地时,就地服务而非再次转发。
func TestRouter_LoopGuard(t *testing.T) {
	members := []shard.Member{mem("a", "http://127.0.0.1:1"), mem("b", "http://127.0.0.1:2")}
	sh := shard.New("a", members)
	var keyB string
	for i := range 1000 {
		k := fmt.Sprintf("k%d", i)
		if o, _ := sh.Owner(k); o.ID() == "b" {
			keyB = k
			break
		}
	}
	if keyB == "" {
		t.Fatal("未找到归属 b 的 key")
	}

	served := false
	rt := shard.NewRouter(sh, shard.PathHeadKey, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served = true
		fmt.Fprint(w, "LOCAL")
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/"+keyB+"/x", nil)
	req.Header.Set("X-Beauty-Shard-Proxied", "b") // 模拟已被别处代理过来
	rt.ServeHTTP(rec, req)
	if !served || rec.Body.String() != "LOCAL" {
		t.Fatalf("防环:带标记的非本地请求应就地服务, served=%v body=%q", served, rec.Body.String())
	}
}

// TestSharder_MembershipChange:成员变更后归属随之更新。
func TestSharder_MembershipChange(t *testing.T) {
	s := shard.New("a", []shard.Member{mem("a", "a:1")})
	// 只有 a 时,一切归 a(本地)。
	if !s.IsLocal("x") {
		t.Fatal("单成员时应本地")
	}
	// 加入 b、c 后,部分 key 迁走。
	s.SetMembers([]shard.Member{mem("a", "a:1"), mem("b", "b:1"), mem("c", "c:1")})
	moved := 0
	for i := range 300 {
		if !s.IsLocal(fmt.Sprintf("x%d", i)) {
			moved++
		}
	}
	if moved == 0 {
		t.Fatal("扩容后应有 key 迁到其他实例")
	}
}

func get(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 256)
	n, _ := resp.Body.Read(buf)
	return string(buf[:n])
}
