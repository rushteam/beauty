package memoryvector_test

import (
	"context"
	"testing"

	"github.com/rushteam/beauty/contrib/llm/agent/memory"
	"github.com/rushteam/beauty/contrib/memoryvector"
	"github.com/rushteam/beauty/contrib/vector"
)

// fakeEmbedder: 用内容哈希出固定维向量,相同文本相同向量。
type fakeEmbedder struct{}

func (fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, 8)
		for j, c := range t {
			v[j%8] += float32(c) / 1000
		}
		out[i] = v
	}
	return out, nil
}

func TestSemanticSearch(t *testing.T) {
	store, err := memoryvector.New(fakeEmbedder{}, vector.NewMemoryStore())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	a := &memory.Item{UserID: "u1", Content: "用户喜欢美式咖啡"}
	b := &memory.Item{UserID: "u1", Content: "用户住在上海"}
	if err := store.Add(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := store.Add(ctx, b); err != nil {
		t.Fatal(err)
	}
	if a.ID == "" || b.ID == "" {
		t.Fatal("ids not assigned")
	}
	hits, err := store.Search(ctx, "u1", "咖啡", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("expected hits")
	}
	// 最相似应偏向咖啡那条(同一 fake embedder 下含相同汉字会更近)
	found := false
	for _, h := range hits {
		if h.Content == "用户喜欢美式咖啡" {
			found = true
		}
	}
	if !found {
		t.Fatalf("hits=%+v", hits)
	}
	if err := store.Delete(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
}
