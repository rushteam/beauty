# Sensitive-Word Filtering: Aho-Corasick + HTTP Middleware

Match thousands of banned words in a single scan, defeat evasion variants, and plug straight into the HTTP layer.

- Algorithm: [`pkg/foundation/actrie`](../pkg/foundation/actrie) — example [actrie](../examples/actrie)
- Middleware: [`pkg/middleware/sensitive`](../pkg/middleware/sensitive) — example [sensitive-middleware](../examples/sensitive-middleware)

---

## 1. actrie: Aho-Corasick automaton

Matches all patterns in **one scan**, O(text length + hits), independent of dictionary size. Built over runes, so CJK works out of the box.

### String Matcher

```go
import "github.com/rushteam/beauty/pkg/foundation/actrie"

m := actrie.New()
m.AddAll("敏感词", "违禁", "测试")

m.Contains(text)     // any hit? (zero alloc when no normalizer)
m.FindAll(text)      // all hits []Match{Pattern, Start, End} (rune indices)
m.FindFirst(text)    // first hit
m.Count(text)        // per-word counts
m.Replace(text, '*') // equal-length masking
```

### Normalization: defeating evasion

Production filtering must resist `c0l0r`, `F*u*c*k`, `ＣＯＬＯＲ` (fullwidth), `CОLОR` (Cyrillic homographs). Pass a rune-level normalizer via `WithNormalizer` (return 0 to skip a char):

```go
norm := actrie.Chain(
    actrie.Keep(func(r rune) bool { // keep CJK/letters/digits, strip * - spaces
        return unicode.Is(unicode.Han, r) || unicode.IsLetter(r) || unicode.IsDigit(r)
    }),
    actrie.VisualMap(), // 0→o @→a fullwidth→ascii Cyrillic→ascii, and lowercases
)
m := actrie.New(actrie.WithNormalizer(norm))
m.Add("敏感词")
m.Contains("敏-感-词") // true
```

Built-ins: `FoldCase`, `Keep(pred)`, `VisualMap`, `Chain`.

**Key**: even when normalization skips or replaces characters, hit positions `Start/End` still map back to the **original** rune indices, so `Replace` masks the correct original span. Length-changing preprocessing (NFKD, multi-char `vv→w`) is not built in since it breaks position mapping — preprocess before feeding if needed.

### Generic Dict[T]: words carry metadata

Attach a payload (category/level/source/replacement) to each word and get it directly on a hit:

```go
type meta struct{ Category string; Level int }

d := actrie.NewDict[meta]()
d.Add("敏感", meta{Category: "politics", Level: 3})
d.Add("广告", meta{Category: "ads", Level: 1})

out := d.ReplaceFunc("含敏感内容和广告", func(mt actrie.DictMatch[meta]) string {
    if mt.Payload.Level >= 3 { return "[blocked]" }
    return "**"
})
```

The string `Matcher` is a thin facade over `Dict[struct{}]`; both share the same algorithm and normalization.

Concurrency: after Build (auto-triggered by queries) all query methods are read-only and safe for concurrent use; construction is not.

---

## 2. sensitive: HTTP middleware

Block or mask banned words in request bodies, plug-and-play. Signature: `func(http.Handler) http.Handler`.

```go
import "github.com/rushteam/beauty/pkg/middleware/sensitive"

mux.Use(sensitive.Block("敏感词", "违禁")) // 403 on hit
mux.Use(sensitive.Mask("敏感词", "违禁"))  // mask and pass through (handler reads clean text)
```

Full config (reuse dictionary, anti-evasion, custom response/audit):

```go
m := actrie.New(actrie.WithNormalizer(norm))
m.AddAll(words...)

mux.Use(sensitive.Middleware(sensitive.Config{
    Matcher:   m,
    Mode:      sensitive.ModeBlock, // or ModeMask
    ScanQuery: true,                // also scan URL query (block-detection only)
    SkipPaths: []string{"/health", "/metrics"},
    OnBlocked: func(w http.ResponseWriter, r *http.Request, hit string) {
        log.Warn("blocked", "hit", hit, "path", r.URL.Path) // hit for audit only, not to client
        w.WriteHeader(http.StatusForbidden)
    },
}))
```

Behavior:

- **Text types only**: `application/json`, `text/*`, `x-www-form-urlencoded`; others pass through.
- **Large-body guard**: bodies over `MaxScanBytes` (default 4MB) pass through untouched (detection scans the prefix, masking is skipped), so large uploads are not broken.
- **Mask mode** rewrites the body and `Content-Length`; the handler reads clean text.
- **Safe default**: the default 403 body is generic and never leaks the hit word to the client; the hit is delivered only to `OnBlocked` for auditing.
- **Anti-evasion** fully reuses `actrie.WithNormalizer`.
- `Matcher == nil` passes through (easy env toggling).
