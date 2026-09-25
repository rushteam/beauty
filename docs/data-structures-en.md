# Data Structures: Filters / Skip List / Diff

Three pure-stdlib, zero-dependency data structures under `pkg/foundation`, covering membership/dedup, ordered indexing, and diffing.

- Probabilistic filters: [`pkg/foundation/filter`](../pkg/foundation/filter) — example [filter](../examples/filter)
- Ordered skip list: [`pkg/foundation/skiplist`](../pkg/foundation/skiplist) — example [skiplist](../examples/skiplist)
- Diff/Patch: [`pkg/foundation/diff`](../pkg/foundation/diff) — example [diff](../examples/diff)

---

## 1. Probabilistic filters

Answer "the element is **definitely absent** / **possibly present**" with tiny memory. Great for cache-penetration guards, massive dedup, URL/fingerprint checks.

| Type | Delete | Use |
|---|---|---|
| `BloomFilter` | no | Most memory-efficient membership; add-only |
| `CuckooFilter` | yes | When you need deletion; better locality at low load |

```go
import "github.com/rushteam/beauty/pkg/foundation/filter"

bf := filter.NewBloom(100000, 0.01) // ~100k items, target FP rate 1%
bf.AddString("user:1")
bf.TestString("user:1")   // true  (possibly present → check cache/DB)
bf.TestString("user:999") // false (definitely absent → reject early)

cf := filter.NewCuckoo(100000)
cf.AddString("spam@evil.com")
cf.ContainsString("spam@evil.com") // true
cf.DeleteString("spam@evil.com")   // Bloom cannot do this
```

Notes:

- Bloom has **no false negatives** (an "absent" answer is always correct); false positives are possible. Observe health via `FillRatio` / `EstimatedFalsePositiveRate`.
- Cuckoo `Add` may fail (returns `false`) near full; reserve capacity, watch `LoadFactor`.
- Neighbors: `pkg/utils/bloom` is Redis-backed (cross-instance); `pkg/foundation/bitmap` is exact (dense IDs, enumerable); `pkg/foundation/sketch` estimates cardinality/frequency (HLL/CountMin). This package is in-memory membership.
- Not concurrency-safe (lock upstream, or one writer per instance).

---

## 2. Ordered skip list

Generic ordered map with expected O(log n) get/insert/delete, plus **rank** and **range** queries — the base for leaderboards, lightweight ordered indexes, Redis ZSET-like structures.

```go
import (
    "cmp"
    "github.com/rushteam/beauty/pkg/foundation/skiplist"
)

sl := skiplist.New[int, string](cmp.Compare[int]) // pass a key comparator
sl.Set(100, "a")
sl.Set(50, "b")

v, ok := sl.Get(100)
sl.Rank(100)             // rank (# of smaller elements + 1)
k, v, ok := sl.ByRank(1) // 1st by rank
sl.Min(); sl.Max()
sl.RangeFrom(50, func(k int, v string) bool { return true }) // ascending from >=50
```

Notes:

- Ordered by `cmp func(a, b K) int`, so any type (incl. struct composite keys) works — no `constraints.Ordered` requirement.
- Keys are unique; `Set` overwrites. `Rank`/`ByRank` are O(log n) via per-level spans.
- Not concurrency-safe. For leaderboards, use a "score-first" struct as key (see the [skiplist example](../examples/skiplist)).

---

## 3. Diff / Patch

Computes the minimal edit script (keep/delete/insert) to turn `a` into `b` via LCS, and applies the patch back. For config rollout, audit logs, collaborative editing, idempotent replay.

```go
import "github.com/rushteam/beauty/pkg/foundation/diff"

patch := diff.DiffLines(oldConf, newConf)
fmt.Print(diff.Format(patch))        // " " unchanged, "-" delete, "+" insert
fmt.Print(diff.FormatCompact(patch)) // changed lines only
eq, ins, del := patch.Stats()

got, err := diff.ApplyLines(oldConf, patch) // → newConf
```

Notes:

- Generic `diff.Diff[T comparable](a, b []T)` works on any comparable elements (lines, tokens, IDs, runes); `DiffLines`/`ApplyLines` are line-based helpers.
- `Apply` performs **baseline-drift detection**: a mismatching patch returns `*MismatchError` instead of failing silently — ideal for idempotent rollout checks.
- LCS DP (O(n·m)), fine for config/doc sizes; use Myers for very large inputs or minimal edit distance.
- Stateless pure functions, inherently concurrency-safe.
