# 敏感词过滤:AC 自动机 + HTTP 中间件

一次扫描匹配成千上万敏感词、对抗变体绕过、并即插即用到 HTTP 层。

- 算法:[`pkg/foundation/actrie`](../pkg/foundation/actrie) — 示例 [actrie](../examples/actrie)
- 中间件:[`pkg/middleware/sensitive`](../pkg/middleware/sensitive) — 示例 [sensitive-middleware](../examples/sensitive-middleware)

---

## 1. actrie:Aho-Corasick 自动机

在一段文本里**一次扫描**同时匹配全部模式串,复杂度 O(文本长度 + 命中数),与词典大小无关。按 rune 构建,天然支持中文。

### 字符串版 Matcher

```go
import "github.com/rushteam/beauty/pkg/foundation/actrie"

m := actrie.New()
m.AddAll("敏感词", "违禁", "测试")

m.Contains(text)         // 是否含任一词(无归一化时零内存分配)
m.FindAll(text)          // 全部命中 []Match{Pattern, Start, End}(rune 下标)
m.FindFirst(text)        // 第一个命中
m.Count(text)            // 各词出现次数 map[string]int
m.Replace(text, '*')     // 等长遮罩:这段包含***和**内容
```

### 归一化:对抗绕过

生产敏感词过滤必须对抗 `c0l0r`、`F*u*c*k`、`ＣＯＬＯＲ`(全角)、`CОLОR`(西里尔同形字)。用 `WithNormalizer` 传入 rune 级归一化函数(返回 0 表示跳过该字符):

```go
norm := actrie.Chain(
    actrie.Keep(func(r rune) bool { // 只保留中文/字母/数字,剥离 * - 空格
        return unicode.Is(unicode.Han, r) || unicode.IsLetter(r) || unicode.IsDigit(r)
    }),
    actrie.VisualMap(), // 视觉映射:0→o @→a 全角→半角 西里尔同形→ASCII,并转小写
)
m := actrie.New(actrie.WithNormalizer(norm))
m.Add("敏感词")
m.Contains("敏-感-词") // true
```

内置归一化:`FoldCase`(大小写)、`Keep(pred)`(过滤/剥离)、`VisualMap`(视觉混淆表)、`Chain`(串联)。

**关键**:即便归一化跳过或替换了字符,命中位置 `Start/End` 仍精确映射回**原文** rune 下标,故 `Replace` 能正确遮罩原文片段。会改变长度的重处理(NFKD、多字符替换 vv→w)因无法保持位置映射,不内置;需要时喂入前自行预处理。

### 泛型版 Dict[T]:词携带元数据

每个词关联一个 payload(分类/等级/来源/替换文案……),命中时直接拿到,免去命中后再查表:

```go
type meta struct{ Category string; Level int }

d := actrie.NewDict[meta]()
d.Add("敏感", meta{Category: "政治", Level: 3})
d.Add("广告", meta{Category: "营销", Level: 1})

// 据 payload 分级处置
out := d.ReplaceFunc("含敏感内容和广告", func(mt actrie.DictMatch[meta]) string {
    if mt.Payload.Level >= 3 { return "[已屏蔽]" }
    return "**"
}) // → 含[已屏蔽]内容和**
```

字符串版 `Matcher` 即 `Dict[struct{}]` 的外观,两者共享同一套算法与归一化。

并发安全:Build(查询自动触发)后所有查询方法只读,可多 goroutine 并发;构建期非并发安全。

---

## 2. sensitive:HTTP 敏感词中间件

即插即用地拦截或脱敏请求体里的敏感词,中间件签名 `func(http.Handler) http.Handler`。

```go
import "github.com/rushteam/beauty/pkg/middleware/sensitive"

// 一行接入
mux.Use(sensitive.Block("敏感词", "违禁")) // 命中即 403
mux.Use(sensitive.Mask("敏感词", "违禁"))  // 命中打码后放行(业务侧读到干净文本)
```

完整配置(复用词典、反绕过、接管响应/审计):

```go
m := actrie.New(actrie.WithNormalizer(norm))
m.AddAll(words...)

mux.Use(sensitive.Middleware(sensitive.Config{
    Matcher:   m,
    Mode:      sensitive.ModeBlock, // 或 ModeMask
    ScanQuery: true,                // 也扫描 URL query(仅拦截检测)
    SkipPaths: []string{"/health", "/metrics"},
    OnBlocked: func(w http.ResponseWriter, r *http.Request, hit string) {
        log.Warn("blocked", "hit", hit, "path", r.URL.Path) // hit 只记审计,不回客户端
        w.WriteHeader(http.StatusForbidden)
    },
}))
```

行为要点:

- **只扫文本类**:`application/json`、`text/*`、`x-www-form-urlencoded`;其余 Content-Type 透传。
- **大体保护**:超过 `MaxScanBytes`(默认 4MB)按"过大不处理"透传——检测仍扫前缀,脱敏跳过,完整回灌 body,不破坏大上传。
- **脱敏模式**自动改写请求体与 `Content-Length`,业务处理器读到的已是干净文本。
- **安全默认**:默认 403 响应体是通用文案,不把命中词回给客户端;命中词只经 `OnBlocked` 回调供审计。
- **反绕过**完全复用 `actrie.WithNormalizer`,中间件不重复逻辑。
- `Matcher == nil` 时透传,便于按环境开关。
