package vector_test

import (
	"context"
	"testing"

	"github.com/rushteam/beauty/contrib/vector"
)

func TestCosine(t *testing.T) {
	if got := vector.Cosine([]float32{1, 0}, []float32{1, 0}); got < 0.999 {
		t.Fatalf("同向应≈1, got %v", got)
	}
	if got := vector.Cosine([]float32{1, 0}, []float32{0, 1}); got > 0.001 || got < -0.001 {
		t.Fatalf("正交应≈0, got %v", got)
	}
	if got := vector.Cosine([]float32{1, 0}, []float32{0, 0}); got != 0 {
		t.Fatalf("零向量应 0, got %v", got)
	}
	if got := vector.Cosine([]float32{1}, []float32{1, 2}); got != 0 {
		t.Fatalf("不等长应 0, got %v", got)
	}
}

func TestMemoryStore(t *testing.T) {
	ctx := context.Background()
	s := vector.NewMemoryStore()

	err := s.Upsert(ctx,
		vector.Document{ID: "a", Vector: []float32{1, 0, 0}, Content: "apple", Metadata: map[string]string{"k": "1"}},
		vector.Document{ID: "b", Vector: []float32{0, 1, 0}, Content: "banana"},
		vector.Document{ID: "c", Vector: []float32{0.9, 0.1, 0}, Content: "apricot"},
	)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if s.Len() != 3 {
		t.Fatalf("len = %d, want 3", s.Len())
	}

	// 查询接近 a 的向量:top2 应是 a、c(都偏 x 轴),b 垫底。
	got, err := s.Query(ctx, []float32{1, 0, 0}, 2)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("topK=2 应返回 2 条, got %d", len(got))
	}
	if got[0].ID != "a" {
		t.Fatalf("最相似应是 a, got %s", got[0].ID)
	}
	if got[1].ID != "c" {
		t.Fatalf("次相似应是 c, got %s", got[1].ID)
	}
	if got[0].Content != "apple" || got[0].Metadata["k"] != "1" {
		t.Fatalf("命中项应带回 content/metadata: %+v", got[0])
	}
	if !(got[0].Score >= got[1].Score) {
		t.Fatalf("应按相似度降序: %v", got)
	}

	// upsert 更新既有 ID。
	_ = s.Upsert(ctx, vector.Document{ID: "a", Vector: []float32{0, 0, 1}, Content: "avocado"})
	if s.Len() != 3 {
		t.Fatalf("更新不应新增, len=%d", s.Len())
	}

	// 删除。
	_ = s.Delete(ctx, "a", "b")
	if s.Len() != 1 {
		t.Fatalf("删除后 len=%d, want 1", s.Len())
	}

	// 维度不匹配的存量被跳过(不 panic)。
	_ = s.Upsert(ctx, vector.Document{ID: "d", Vector: []float32{1, 1}})
	if _, err := s.Query(ctx, []float32{1, 0, 0}, 5); err != nil {
		t.Fatalf("混维查询应健壮: %v", err)
	}
}
