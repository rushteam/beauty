package router

import "testing"

func TestRouteCodec_RegisterAndEncodeDecode(t *testing.T) {
	c := NewRouteCodec()
	n := c.Register("game.move", "game.sync", "chat.send")
	if n != 3 {
		t.Fatalf("新增=%d, want 3", n)
	}
	if c.Len() != 3 {
		t.Fatalf("Len=%d, want 3", c.Len())
	}

	// 编码
	k, ok := c.Encode("game.move")
	if !ok || k != 1 {
		t.Fatalf("Encode(game.move)=%d,%v, want 1,true", k, ok)
	}
	k, ok = c.Encode("chat.send")
	if !ok || k != 3 {
		t.Fatalf("Encode(chat.send)=%d,%v, want 3,true", k, ok)
	}

	// 解码
	r, ok := c.Decode(2)
	if !ok || r != "game.sync" {
		t.Fatalf("Decode(2)=%q,%v, want game.sync,true", r, ok)
	}

	// 不存在
	_, ok = c.Encode("no.such")
	if ok {
		t.Fatal("不存在的路由应返回 false")
	}
	_, ok = c.Decode(999)
	if ok {
		t.Fatal("不存在的 kind 应返回 false")
	}
}

func TestRouteCodec_RegisterIdempotent(t *testing.T) {
	c := NewRouteCodec()
	c.Register("a", "b")
	n := c.Register("a", "b", "c")
	if n != 1 {
		t.Fatalf("重复注册只应新增1个, got %d", n)
	}
	if c.Len() != 3 {
		t.Fatalf("Len=%d, want 3", c.Len())
	}
	// "a" 的 kind 不变
	k, _ := c.Encode("a")
	if k != 1 {
		t.Fatalf("a 的 kind 不应改变, got %d", k)
	}
}

func TestRouteCodec_Table(t *testing.T) {
	c := NewRouteCodec()
	c.Register("x", "y")
	tbl := c.Table()
	if len(tbl) != 2 {
		t.Fatalf("Table len=%d, want 2", len(tbl))
	}
	if tbl["x"] != 1 || tbl["y"] != 2 {
		t.Fatalf("Table=%v", tbl)
	}

	// Table 是快照,修改不影响原始
	tbl["x"] = 999
	k, _ := c.Encode("x")
	if k != 1 {
		t.Fatal("修改快照不应影响 codec")
	}
}

func TestRouteCodec_ReverseTable(t *testing.T) {
	c := NewRouteCodec()
	c.Register("a", "b")
	rt := c.ReverseTable()
	if rt[1] != "a" || rt[2] != "b" {
		t.Fatalf("ReverseTable=%v", rt)
	}
}

func TestRouteCodec_Version_Diff(t *testing.T) {
	c := NewRouteCodec()
	c.Register("a", "b")
	v1 := c.Version()
	if v1 != 2 {
		t.Fatalf("Version=%d, want 2", v1)
	}

	c.Register("c")
	v2 := c.Version()
	if v2 != 3 {
		t.Fatalf("Version=%d, want 3", v2)
	}

	diff := c.Diff(v1)
	if len(diff) != 1 {
		t.Fatalf("Diff 应只有1条增量, got %d", len(diff))
	}
	if diff["c"] != 3 {
		t.Fatalf("Diff=%v", diff)
	}

	// 全量
	full := c.Diff(0)
	if len(full) != 3 {
		t.Fatalf("Diff(0) 应返回全量=%d", len(full))
	}
}
