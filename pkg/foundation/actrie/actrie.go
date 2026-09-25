// Package actrie 提供 Aho-Corasick 自动机(AC 自动机):在一段文本里**一次扫描**同时匹配
// 成千上万个模式串,时间复杂度 O(文本长度 + 命中数),与模式串数量无关。
//
// 它是"敏感词过滤 / 词典分词 / 多关键词高亮 / 静态路由前缀匹配"的标准工具:把 Trie 树
// (前缀树)加上"失配指针"(fail 链,类似 KMP 的 next 数组的多模版本),失配时不必回退
// 文本指针,而是沿 fail 链跳到最长可复用的后缀继续匹配。
//
// 关于"双数组 Trie":双数组是 Trie 的一种**空间压缩存储**(把 goto 表压成两个整型数组,
// 常量级转移、极致省内存),属于工程优化;本包用基于 map 的 goto 表实现,逻辑清晰、
// 构建快、内存可接受(中文敏感词典万级规模足够),需要极致内存/只读大词典时再上双数组。
//
// 按 rune(而非 byte)构建,天然支持中文等多字节字符,不会出现字节级误匹配。
//
// # 两种类型
//
//   - Matcher:字符串版,命中只回报"匹配了哪个词"。用 New 构造。
//   - Dict[T]:泛型版,每个词携带一个 payload T(分类/等级/来源/替换文案……),命中时
//     直接拿到元数据,免去命中后再查表。用 NewDict[T] 构造。Matcher 即 Dict[struct{}] 的外观。
//
// # 归一化(反变体 / 反混淆)
//
// 生产级敏感词过滤要对抗绕过:c0l0r、F*u*c*k、ＣＯＬＯＲ(全角)、CОLОR(西里尔同形字)。
// 用 WithNormalizer 传入一个 rune 级归一化函数,它在 Add(对模式)与查询(对文本)时对每个
// 字符生效:返回归一化后的 rune,或返回 0 表示"跳过该字符"(用于剥离 * - 空格等干扰符)。
// 内置 FoldCase(大小写)、Keep(过滤)、VisualMap(视觉混淆表)、Chain(串联),见 normalizer.go。
//
// 关键:即便归一化跳过了字符(1→0)或做了同形替换(1→1),命中位置 Start/End 仍精确映射回
// **原文** rune 下标,故 Replace 能正确遮罩原文对应片段。会改变长度的重处理(NFKD、多字符
// 替换 vv→w)因无法保持位置映射,不内置;需要时在喂入前自行预处理,或用 contrib。
//
// 用法:New/NewDict(opts...) 后 Add 多个模式(或 AddAll),然后 FindAll/Contains/Replace 等
// 只读查询(未 Build 会自动 Build)。Build 后再 Add 需重新 Build。
//
// 并发安全:Build 完成后所有查询方法只读,可多 goroutine 并发;构建期非并发安全。
// 零值不可用,用 New / NewDict 构造。
package actrie

// Matcher 是字符串版 AC 自动机(命中只回报词本身),是 Dict[struct{}] 的轻量外观。
type Matcher struct {
	d *Dict[struct{}]
}

// New 创建空的字符串自动机。
func New(opts ...Option) *Matcher {
	return &Matcher{d: NewDict[struct{}](opts...)}
}

// Add 添加一个模式串。空串(或归一化后为空)忽略。重复只保留一份。Build 后再 Add 需重新 Build。
func (m *Matcher) Add(pattern string) { m.d.Add(pattern, struct{}{}) }

// AddAll 批量添加模式串。
func (m *Matcher) AddAll(patterns ...string) {
	for _, p := range patterns {
		m.d.Add(p, struct{}{})
	}
}

// Size 返回已添加的(去重后)模式串个数。
func (m *Matcher) Size() int { return m.d.Size() }

// Build 构建 fail 指针与字典后缀链(查询会自动调用)。
func (m *Matcher) Build() { m.d.Build() }

// Match 表示一次命中:模式串及其在原文本中的 rune 起止下标 [Start, End)。
type Match struct {
	Pattern string
	Start   int // 原文 rune 下标(非 byte)
	End     int // 原文 rune 下标(不含)
}

// FindAll 返回全部命中(含重叠),按结束位置升序。
func (m *Matcher) FindAll(text string) []Match {
	dm := m.d.FindAll(text)
	if len(dm) == 0 {
		return nil
	}
	out := make([]Match, len(dm))
	for i, x := range dm {
		out[i] = Match{Pattern: x.Word, Start: x.Start, End: x.End}
	}
	return out
}

// Contains 判断文本是否含任一模式串(短路)。无归一化时零内存分配。
func (m *Matcher) Contains(text string) bool { return m.d.Contains(text) }

// FindFirst 返回第一个命中(结束位置最早),没有则 ok=false。
func (m *Matcher) FindFirst(text string) (Match, bool) {
	x, ok := m.d.FindFirst(text)
	if !ok {
		return Match{}, false
	}
	return Match{Pattern: x.Word, Start: x.Start, End: x.End}, true
}

// Count 统计每个模式串出现次数(含重叠)。
func (m *Matcher) Count(text string) map[string]int { return m.d.Count(text) }

// Replace 把所有命中替换为等长 mask 字符,重叠命中合并为连续遮罩。
func (m *Matcher) Replace(text string, mask rune) string { return m.d.Replace(text, mask) }

// ReplaceFunc 用回调决定每个命中的替换文本(分级遮罩/打标/脱敏)。命中按起点升序、
// 同起点长者优先,与已替换区间重叠者跳过。替换文本可不等长。
func (m *Matcher) ReplaceFunc(text string, repl func(Match) string) string {
	return m.d.ReplaceFunc(text, func(x DictMatch[struct{}]) string {
		return repl(Match{Pattern: x.Word, Start: x.Start, End: x.End})
	})
}
