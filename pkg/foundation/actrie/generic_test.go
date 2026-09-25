package actrie

import (
	"testing"
	"unicode"
)

// 敏感词元数据
type word struct {
	Category string
	Level    int
}

func TestDictPayloadRetrieval(t *testing.T) {
	d := NewDict[word]()
	d.Add("敏感", word{Category: "政治", Level: 3})
	d.Add("广告", word{Category: "营销", Level: 1})

	matches := d.FindAll("含敏感内容和广告")
	if len(matches) != 2 {
		t.Fatalf("want 2 matches, got %d", len(matches))
	}
	byWord := map[string]word{}
	for _, m := range matches {
		byWord[m.Word] = m.Payload
	}
	if byWord["敏感"].Category != "政治" || byWord["敏感"].Level != 3 {
		t.Fatalf("敏感 payload=%+v", byWord["敏感"])
	}
	if byWord["广告"].Category != "营销" || byWord["广告"].Level != 1 {
		t.Fatalf("广告 payload=%+v", byWord["广告"])
	}
}

func TestDictReplaceFuncByLevel(t *testing.T) {
	d := NewDict[word]()
	d.Add("敏感", word{Level: 3})
	d.Add("广告", word{Level: 1})
	got := d.ReplaceFunc("含敏感内容和广告", func(m DictMatch[word]) string {
		if m.Payload.Level >= 3 {
			return "[已屏蔽]"
		}
		return "**"
	})
	if want := "含[已屏蔽]内容和**"; got != want {
		t.Fatalf("ReplaceFunc=%q want %q", got, want)
	}
}

func TestDictFindFirstAndCount(t *testing.T) {
	d := NewDict[int]()
	d.Add("ab", 1)
	d.Add("bc", 2)
	m, ok := d.FindFirst("xabc")
	if !ok || m.Word != "ab" || m.Payload != 1 {
		t.Fatalf("FindFirst=%+v,%v", m, ok)
	}
	cnt := d.Count("ababc")
	if cnt["ab"] != 2 || cnt["bc"] != 1 {
		t.Fatalf("Count=%v", cnt)
	}
}

func TestDictDuplicateOverwritesPayload(t *testing.T) {
	d := NewDict[string]()
	d.Add("x", "first")
	d.Add("x", "second")
	if d.Size() != 1 {
		t.Fatalf("Size=%d want 1", d.Size())
	}
	m, _ := d.FindFirst("x")
	if m.Payload != "second" {
		t.Fatalf("payload=%q want second", m.Payload)
	}
}

func TestDictWithNormalizer(t *testing.T) {
	// 泛型版同样支持归一化:剥离干扰符 + 视觉映射
	norm := Chain(Keep(func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }), VisualMap())
	d := NewDict[int](WithNormalizer(norm))
	d.Add("color", 42)
	m, ok := d.FindFirst("say c0l0r!")
	if !ok || m.Payload != 42 {
		t.Fatalf("normalized generic match failed: %+v,%v", m, ok)
	}
	// 位置映射回原文:c0l0r 起于 "say " 之后
	sub := string([]rune("say c0l0r!")[m.Start:m.End])
	if sub != "c0l0r" {
		t.Fatalf("span=%q want c0l0r", sub)
	}
}

// 多租户:同一个词由不同用户添加,payload 记录归属(后者覆盖,演示去重语义)。
func TestDictMultiTenantSemantics(t *testing.T) {
	type owner struct{ UserID int64 }
	d := NewDict[owner]()
	d.Add("苹果", owner{UserID: 1})
	d.Add("苹果", owner{UserID: 2}) // 覆盖
	m, ok := d.FindFirst("我买了苹果")
	if !ok || m.Payload.UserID != 2 {
		t.Fatalf("want userID 2, got %+v,%v", m, ok)
	}
	if d.Size() != 1 {
		t.Fatalf("Size=%d want 1", d.Size())
	}
}
