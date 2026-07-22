package pagination_test

import (
	"testing"

	"github.com/rushteam/beauty/pkg/pagination"
)

type cur struct {
	CreatedAt int64 `json:"c"`
	ID        int64 `json:"i"`
}

func TestEncodeDecode_RoundTrip(t *testing.T) {
	in := cur{CreatedAt: 1720000000, ID: 42}
	s, err := pagination.Encode(in)
	if err != nil {
		t.Fatal(err)
	}
	out, ok, err := pagination.Decode[cur](s)
	if err != nil || !ok {
		t.Fatalf("decode ok=%v err=%v", ok, err)
	}
	if out != in {
		t.Fatalf("往返不一致: %+v vs %+v", out, in)
	}
}

func TestDecode_Empty(t *testing.T) {
	_, ok, err := pagination.Decode[cur]("")
	if ok || err != nil {
		t.Fatalf("空游标应 ok=false,err=nil; got ok=%v err=%v", ok, err)
	}
}

func TestDecode_Malformed(t *testing.T) {
	if _, _, err := pagination.Decode[cur]("!!!not-base64!!!"); err == nil {
		t.Fatal("非法 base64 应报错")
	}
	// 合法 base64 但非 json。
	if _, _, err := pagination.Decode[cur]("aGVsbG8"); err == nil {
		t.Fatal("非 json 应报错")
	}
}

func TestBuild_HasNextPage(t *testing.T) {
	// limit=3,多取一条 → 4 条,应有下一页且截断到 3。
	rows := []cur{{1, 1}, {2, 2}, {3, 3}, {4, 4}}
	page, err := pagination.Build(rows, 3, func(c cur) cur { return c })
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 {
		t.Fatalf("应截断到 3 条, got %d", len(page.Items))
	}
	if page.Next == "" {
		t.Fatal("应有下一页游标")
	}
	// Next 应指向第 3 条(最后一条返回项)。
	c, _, _ := pagination.Decode[cur](page.Next)
	if c != (cur{3, 3}) {
		t.Fatalf("Next 游标应指向第 3 条, got %+v", c)
	}
}

func TestBuild_LastPage(t *testing.T) {
	rows := []cur{{1, 1}, {2, 2}}
	page, err := pagination.Build(rows, 3, func(c cur) cur { return c })
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Next != "" {
		t.Fatalf("最后一页应无 Next, items=%d next=%q", len(page.Items), page.Next)
	}
}
