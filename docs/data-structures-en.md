# Data Structures: Filters / Skip List / Bitmap / Interval Tree / Similarity / CRDT / Diff

A set of **pure-stdlib, zero/low-dependency** data structures and algorithms under `pkg/foundation` (and `pkg/idgen`), each solving one general problem. Mechanisms, not policy — usable directly in core or composed by higher layers.

| Package | Problem | Example |
|---|---|---|
| [`filter`](../pkg/foundation/filter) | Membership/dedup (Bloom/Cuckoo: definitely-absent / possibly-present) | [filter](../examples/filter) |
| [`skiplist`](../pkg/foundation/skiplist) | Ordered map + rank + range (leaderboards/ordered index) | [skiplist](../examples/skiplist) |
| [`roaring`](../pkg/foundation/roaring) | Compressed bitmap for huge sparse int sets (adaptive containers + **official cross-language format**) | [roaring](../examples/roaring) |
| [`intervaltree`](../pkg/foundation/intervaltree) | Interval overlap / point-stabbing (schedules, IP ranges, timelines) | [intervaltree](../examples/intervaltree) |
| [`sketch`](../pkg/foundation/sketch) | Probabilistic estimates: cardinality HLL, frequency CountMin, **quantiles TDigest** | [tdigest](../examples/tdigest) |
| [`simhash`](../pkg/foundation/simhash) | Near-duplicate detection (SimHash fingerprint / MinHash Jaccard) | [simhash](../examples/simhash) |
| [`crdt`](../pkg/foundation/crdt) | Conflict-free replication (multi-device/offline-first, with **delta-state sync**) | [crdt](../examples/crdt) |
| [`diff`](../pkg/foundation/diff) | Diff/patch: LCS, **Myers**, **JSON Patch RFC6902**, **3-way merge** | [diff](../examples/diff) · [jsonpatch](../examples/jsonpatch) |
| [`actrie`](../pkg/foundation/actrie) | Multi-pattern match + double-array Trie (with **AC normalization anti-evasion**) | [actrie](../examples/actrie) · [dat](../examples/dat) |
| [`idgen`](../pkg/idgen) | Distributed ID segment mode (double-buffered prefetch, roughly increasing) | [idgen](../examples/idgen) · [segment](../examples/segment) |

> For sensitive-word actrie usage see the dedicated [sensitive-words-en](sensitive-words-en.md). This doc covers the rest; every package ships full godoc — `go doc github.com/rushteam/beauty/pkg/foundation/<pkg>` for a quick look.

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
- LCS is DP (O(n·m)); for minimal edit distance / large inputs use **`DiffMyers` / `DiffMyersLines`** (Myers O(ND)).
- **JSON Patch (RFC6902)**: `diff.JSONDiff(a, b)` emits add/remove/replace/move/copy/test; `diff.ApplyJSON(doc, patch)` applies it. JSON Pointer (RFC6901), arrays aligned via LCS for minimal patches. See [jsonpatch](../examples/jsonpatch).
- **3-way merge**: `diff.Merge3[T](base, a, b)` / `diff.Merge3Lines` (diff3) auto-merges non-overlapping edits and reports `Conflict` on overlap — the basis for collaborative/concurrent config edits.
- Stateless pure functions, inherently concurrency-safe.

---

## 4. Compressed bitmap `roaring`

Roaring Bitmap: bucket by the high 16 bits, each bucket adapts among **array / bitmap / run** containers — stores huge sparse int sets exactly and does fast set ops. Used for tag systems, permission bits, inverted indexes, user segmentation.

```go
import "github.com/rushteam/beauty/pkg/foundation/roaring"

b := roaring.New()
for i := uint32(0); i < 1_000_000; i++ { b.Add(i) } // 1M contiguous IDs
b.RunOptimize()                 // squeeze runs into run containers

a := roaring.Of(1, 2, 3)
a.And(roaring.Of(2, 3, 4))      // intersection → {2,3}; also Or / AndNot

// Cross-language: official RoaringFormatSpec, readable by CRoaring/Java/pyroaring
blob, _ := b.MarshalRoaring()
var b2 roaring.Bitmap
_ = (&b2).UnmarshalRoaring(blob)
```

Notes:

- After `RunOptimize()` contiguous ranges shrink drastically (1M contiguous IDs: ~128KB → a few hundred bytes).
- Two serializers: `MarshalBinary` (private compact, in-house) and **`MarshalRoaring` (official cross-language)** interoperable with C/C++/Java/Python (Kafka/Parquet/on-disk sharing).
- Not concurrency-safe; read-only bitmaps allow concurrent queries.

---

## 5. Interval tree `intervaltree`

Store intervals `[Low, High]` and answer "which overlap [x,y]" / "which cover point p" efficiently. Backed by a treap + maxHigh augmentation. Meeting/room conflicts, IP-range lookup, timeline overlap, genomic intervals.

```go
import "github.com/rushteam/beauty/pkg/foundation/intervaltree"

t := intervaltree.New[string]()
t.Insert(intervaltree.Interval{Low: 10, High: 20}, "A")
t.Insert(intervaltree.Interval{Low: 15, High: 25}, "B")

t.Stab(18)                                           // intervals covering 18 → A, B
t.Overlap(intervaltree.Interval{Low: 22, High: 30}) // overlapping [22,30] → B
t.Any(intervaltree.Interval{Low: 0, High: 5})       // existence → false
```

Notes:

- `Stab(p)`, `Overlap(iv)`, `Any(iv)` short-circuit; `Insert`/`Remove` expected O(log n). Generic `Tree[V]` carries a payload per interval. Not concurrency-safe.

---

## 6. Probabilistic sketches `sketch`

Approximate stats over large streams in fixed small memory: HyperLogLog (cardinality), Count-Min (frequency/Top-K), reservoir sampling, and **TDigest (streaming quantiles)**.

```go
import "github.com/rushteam/beauty/pkg/foundation/sketch"

td := sketch.NewTDigest(100) // compression; higher = more accurate
for _, ms := range latencies { td.Add(ms) }
p99 := td.Quantile(0.99)

// Cross-process merge: per-instance stats, merge at the aggregator
blob, _ := td.MarshalJSON()
var agg sketch.TDigest
_ = agg.UnmarshalJSON(blob)
```

Notes:

- TDigest is accurate at the tail (P99/P999) and mergeable (multi-instance aggregation) — the go-to for latency distributions. All "accuracy for memory": bounded error, O(1) space.

---

## 7. Near-similarity `simhash`

Decide how similar two texts/sets are, for **near-duplicate detection**: crawler dedup, plagiarism, log clustering, spam filtering.

```go
import "github.com/rushteam/beauty/pkg/foundation/simhash"

a := simhash.Hash(tokensA...) // 64-bit fingerprint
b := simhash.Hash(tokensB...)
simhash.Distance(a, b)        // Hamming distance, smaller = more similar
simhash.Similar(a, b, 3)      // distance ≤ 3 → near-duplicate

mh := simhash.NewMinHash(128)
simhash.Jaccard(mh.Signature(setA), mh.Signature(setB))
```

Notes:

- SimHash compresses a doc to 64 bits; Hamming ≈ semantic distance, good for massive pairwise dedup (with LSH bucketing). MinHash signatures estimate Jaccard for set similarity.

---

## 8. Conflict-free replication `crdt`

CRDTs let replicas update independently and converge after out-of-order exchange — **no central coordination, no locking**. The math basis for multi-device collaboration, offline-first, and eventual consistency under weak networks. Merge is commutative/associative/idempotent, never conflicts (unlike 3-way merge).

```go
import "github.com/rushteam/beauty/pkg/foundation/crdt"

a := crdt.NewPNCounter()
b := crdt.NewPNCounter()
a.Inc("node-a", 10); b.Dec("node-b", 3)

// delta-state sync: send only "changes since last", far less bandwidth than full state
b.MergeDelta(a.Delta())
a.MergeDelta(b.Delta())
fmt.Println(a.Value(), b.Value()) // 7 7 (converged)
```

Members: `GCounter`, `PNCounter`, `GSet`, `ORSet` (add-wins), `LWWRegister`.

Notes:

- Two sync modes: full `Merge` (anti-entropy fallback) and **delta-state `Delta()`/`MergeDelta()`** (bandwidth-saving gossip, same result as full). Each type has JSON serialization. nodeID must be globally unique. State-based CRDTs; serialize local updates, coordinate Merge at the caller.

---

## 9. Distributed ID segments `idgen`

`pkg/idgen` segment mode (Leaf-segment): fetch a **batch (segment)** of IDs from storage into memory per round; double-buffered prefetch means almost no blocking. Unlike Snowflake it has no clock dependency, shorter and roughly increasing IDs (B+Tree-index friendly).

```go
import "github.com/rushteam/beauty/pkg/idgen"

seg := idgen.NewSegment(store, "order") // store implements SegmentStore.NextRange
id, _ := seg.Next()                      // in-memory increment, hits storage only when a segment is exhausted
```

Notes:

- `SegmentStore` is the storage interface; `MemorySegmentStore` for local tests; for production see [`contrib/gorm`](../contrib/gorm) `SegmentStore` (one atomic `UPDATE max_id = max_id + step` per segment).
- Double buffer + `WithPreloadThreshold`: prefetch the next segment at a threshold to smooth spikes. IDs are roughly increasing (not strictly contiguous): restarts skip unused segments, trading gaps for HA and low lock contention.
