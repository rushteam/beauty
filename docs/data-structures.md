# 基础数据结构:过滤器 / 跳表 / 位图 / 区间树 / 相似度 / CRDT / 差异

`pkg/foundation`(与 `pkg/idgen`)下一组**纯标准库、零/极少依赖**的数据结构与算法,各自解决一类通用问题。均为机制而非策略,核心可直接用、也可被上层组合。

| 包 | 解决的问题 | 示例 |
|---|---|---|
| [`filter`](../pkg/foundation/filter) | 判存去重(Bloom/Cuckoo,一定不存在/可能存在) | [filter](../examples/filter) |
| [`skiplist`](../pkg/foundation/skiplist) | 有序映射 + 名次 + 范围(排行榜/有序索引) | [skiplist](../examples/skiplist) |
| [`roaring`](../pkg/foundation/roaring) | 海量稀疏整数集的压缩位图(自适应容器 + **官方跨语言格式**) | [roaring](../examples/roaring) |
| [`intervaltree`](../pkg/foundation/intervaltree) | 区间相交 / 点覆盖查询(日程冲突、IP 段、时间轴) | [intervaltree](../examples/intervaltree) |
| [`sketch`](../pkg/foundation/sketch) | 概率估计:基数 HLL、频率 CountMin、**分位数 TDigest** | [tdigest](../examples/tdigest) |
| [`simhash`](../pkg/foundation/simhash) | 近似相似去重(SimHash 指纹 / MinHash Jaccard) | [simhash](../examples/simhash) |
| [`crdt`](../pkg/foundation/crdt) | 无冲突复制(多端协同/离线优先,含 **delta-state 增量同步**) | [crdt](../examples/crdt) |
| [`diff`](../pkg/foundation/diff) | 差异/补丁:LCS、**Myers**、**JSON Patch RFC6902**、**三路合并** | [diff](../examples/diff) · [jsonpatch](../examples/jsonpatch) |
| [`actrie`](../pkg/foundation/actrie) | 多模匹配 + 双数组 Trie(敏感词/词典,含 **AC 归一化反绕过**) | [actrie](../examples/actrie) · [dat](../examples/dat) |
| [`idgen`](../pkg/idgen) | 分布式 ID 号段模式(双缓冲预取,近似递增) | [idgen](../examples/idgen) · [segment](../examples/segment) |

> 敏感词 actrie 的用法另见专题 [sensitive-words](sensitive-words.md)。本文覆盖其余数据结构;各包均有完整中文 godoc,`go doc github.com/rushteam/beauty/pkg/foundation/<pkg>` 可速查。

---

## 1. 概率过滤器 filter

用极小内存回答"这个元素**一定不存在** / **可能存在**"。适合缓存穿透防护、海量去重、URL/指纹判重。

| 类型 | 支持删除 | 适用 |
|---|---|---|
| `BloomFilter` | 否 | 最省内存的判存;只增不删 |
| `CuckooFilter` | 是 | 需要删除元素;低负载空间/查询更优 |

```go
import "github.com/rushteam/beauty/pkg/foundation/filter"

// 布隆:预期 10w 元素,目标误判率 1%
bf := filter.NewBloom(100000, 0.01)
bf.AddString("user:1")
bf.TestString("user:1")   // true(可能存在→继续查缓存/DB)
bf.TestString("user:999") // false(一定不存在→直接挡掉)

// 布谷鸟:支持删除
cf := filter.NewCuckoo(100000)
cf.AddString("spam@evil.com")
cf.ContainsString("spam@evil.com") // true
cf.DeleteString("spam@evil.com")   // 布隆做不到
```

要点:

- 布隆**无假阴性**(说"不存在"就一定不存在),有假阳性(说"存在"可能是误报),`FillRatio` / `EstimatedFalsePositiveRate` 可观测健康度。
- 布谷鸟高负载(接近满)时 `Add` 可能失败(返回 `false`),需预留容量;`LoadFactor` 观测负载。
- 与相邻包:`pkg/utils/bloom` 是 Redis 版(跨实例共享);`pkg/foundation/bitmap` 是精确型(ID 稠密可枚举);`pkg/foundation/sketch` 估计基数/频率(HLL/CountMin)。本包是纯内存判存。
- 均非并发安全(读多写少上层加锁,或每个写者独占一份)。

---

## 2. 有序跳表 skiplist

泛型有序映射,期望 O(log n) 点查/插入/删除,额外支持**名次**与**范围遍历**——排行榜、轻量有序索引、Redis ZSET 式结构的底座。

```go
import (
    "cmp"
    "github.com/rushteam/beauty/pkg/foundation/skiplist"
)

sl := skiplist.New[int, string](cmp.Compare[int]) // 传入键比较函数
sl.Set(100, "a")
sl.Set(50, "b")

v, ok := sl.Get(100)     // 点查
sl.Rank(100)             // 名次(比它小的元素数 +1)
k, v, ok := sl.ByRank(1) // 第 1 名(名次 → 键值)
sl.Min(); sl.Max()       // 最小/最大
sl.RangeFrom(50, func(k int, v string) bool { return true }) // 从 >=50 升序遍历
```

要点:

- 用比较函数 `cmp func(a, b K) int` 定序,可对任意类型(含结构体复合键)排序,不要求 `constraints.Ordered`。
- 键唯一,`Set` 同键覆盖值;`Rank`/`ByRank` 基于每层 span 维护,O(log n)。
- 非并发安全。排行榜可把"分数在前"的结构体作 key(见 [skiplist 示例](../examples/skiplist))。

---

## 3. 差异 / 补丁 diff

基于最长公共子序列(LCS)算出把序列 a 变成 b 的最小编辑脚本(保留/删除/插入),并能把补丁应用回 a。配置中心动态下发、操作审计、协同编辑、幂等回放。

```go
import "github.com/rushteam/beauty/pkg/foundation/diff"

patch := diff.DiffLines(oldConf, newConf) // 按行 diff
fmt.Print(diff.Format(patch))             // 渲染:空=未变 -=删除 +=新增
fmt.Print(diff.FormatCompact(patch))      // 只看变更行
eq, ins, del := patch.Stats()             // 保留/新增/删除行数

got, err := diff.ApplyLines(oldConf, patch) // 应用补丁 → 得到 newConf
```

要点:

- 泛型 `diff.Diff[T comparable](a, b []T)` 对任意可比较元素(行、token、ID、rune)工作;`DiffLines`/`ApplyLines` 是"按行"的便捷封装。
- `Apply` 会做**基线漂移检测**:补丁与源不匹配(生成补丁后基线又被改过)返回 `*MismatchError` 而非静默出错,适合幂等下发校验。
- LCS 为动态规划(O(n·m));追求最短编辑距离/大输入可用 **`DiffMyers` / `DiffMyersLines`**(Myers O(ND),贪心对角线)。
- **JSON Patch(RFC6902)**:`diff.JSONDiff(a, b)` 生成 add/remove/replace/move/copy/test 操作;`diff.ApplyJSON(doc, patch)` 应用。配 JSON Pointer(RFC6901),数组用 LCS 对齐以生成最小补丁。见示例 [jsonpatch](../examples/jsonpatch)。
- **三路合并**:`diff.Merge3[T](base, a, b)` / `diff.Merge3Lines`(diff3 算法),双方改动不重叠时自动合并,重叠时回报 `Conflict`——协同编辑、配置并发改的基石。
- 无状态、纯函数,天然并发安全。

---

## 4. 压缩位图 roaring

Roaring Bitmap:按高 16 位分桶,每桶在 **array / bitmap / run** 三种容器间自适应,既能精确存海量稀疏整数集,又能做快速集合运算。标签系统、权限位、倒排索引、用户分群常用。

```go
import "github.com/rushteam/beauty/pkg/foundation/roaring"

b := roaring.New()
for i := uint32(0); i < 1_000_000; i++ { b.Add(i) } // 百万连续 ID
b.RunOptimize()                 // 连续区间压成 run 容器
fmt.Println(b.Len())            // 1000000

a := roaring.Of(1, 2, 3)
c := roaring.Of(2, 3, 4)
a.And(c)                        // 交集 → {2,3};还有 Or / AndNot

// 跨语言互操作:官方 RoaringFormatSpec,可被 CRoaring/Java/pyroaring 读取
blob, _ := b.MarshalRoaring()
var b2 roaring.Bitmap
_ = (&b2).UnmarshalRoaring(blob)
```

要点:

- `RunOptimize()` 后连续区间内存骤降(百万连续 ID 从 ~128KB 降到几百字节)。
- 两套序列化:`MarshalBinary`(私有紧凑格式,自用)与 **`MarshalRoaring`(官方跨语言格式)**,后者与 C/C++/Java/Python 生态互读互写(Kafka/Parquet/落盘共享)。
- 非并发安全;只读场景可多协程并发查询。

---

## 5. 区间树 intervaltree

存一堆区间 `[Low, High]`,高效回答"哪些区间与 [x,y] 相交""哪些区间覆盖点 p"。底层是 treap(随机平衡 BST)+ maxHigh 增广。日程/会议室冲突、IP 段归属、时间轴重叠、基因组区间都用它。

```go
import "github.com/rushteam/beauty/pkg/foundation/intervaltree"

t := intervaltree.New[string]()
t.Insert(intervaltree.Interval{Low: 10, High: 20}, "A")
t.Insert(intervaltree.Interval{Low: 15, High: 25}, "B")

t.Stab(18)                                   // 覆盖点 18 的区间 → A、B
t.Overlap(intervaltree.Interval{Low: 22, High: 30}) // 与 [22,30] 相交 → B
t.Any(intervaltree.Interval{Low: 0, High: 5})       // 是否存在相交 → false
```

要点:

- `Stab(p)` 点查、`Overlap(iv)` 区间查、`Any(iv)` 存在性短路;`Insert`/`Remove` 期望 O(log n)。
- 泛型 `Tree[V]` 每个区间挂任意 payload。
- 非并发安全。

---

## 6. 概率估计 sketch

用固定小内存对大数据流做**近似**统计。`sketch` 含:HyperLogLog(基数/去重计数)、Count-Min(频率/Top-K)、蓄水池采样,以及 **TDigest(流式分位数)**。

```go
import "github.com/rushteam/beauty/pkg/foundation/sketch"

// TDigest:流式估计 P50/P99 延迟,内存与数据量无关
td := sketch.NewTDigest(100) // compression,越大越精确
for _, ms := range latencies { td.Add(ms) }
p99 := td.Quantile(0.99)

// 跨进程合并:各实例各自统计,汇总端 Merge
blob, _ := td.MarshalJSON()
var agg sketch.TDigest
_ = agg.UnmarshalJSON(blob)
```

要点:

- TDigest 对尾部分位数(P99/P999)精度高、可合并(多实例汇总),是监控延迟分布的常用结构。
- 全部为"近似换内存":用可控误差换取 O(1) 空间。

---

## 7. 近似相似 simhash

判断两段文本/两个集合"有多像",用于**近重复检测**:爬虫去重、洗稿/抄袭识别、日志聚类、垃圾内容拦截。

```go
import "github.com/rushteam/beauty/pkg/foundation/simhash"

a := simhash.Hash(tokensA...) // 64 位指纹
b := simhash.Hash(tokensB...)
simhash.Distance(a, b)        // 汉明距离,越小越像
simhash.Similar(a, b, 3)      // 距离≤3 视为近重复

// MinHash:估计两个集合的 Jaccard 相似度
mh := simhash.NewMinHash(128)
simhash.Jaccard(mh.Signature(setA), mh.Signature(setB))
```

要点:

- SimHash 把文档压成 64 位指纹,汉明距离近似语义距离,适合海量两两判重(配 LSH 分桶)。
- MinHash 签名估计 Jaccard,适合集合相似(共同好友、标签重合)。

---

## 8. 无冲突复制 crdt

CRDT 让多个副本各自独立更新、乱序交换后仍收敛到同一结果,**无需中心协调、无需加锁**——多端协同、离线优先、多活弱网下"最终一致"的数学基石。Merge 天然满足交换/结合/幂等,永不冲突(区别于需共同祖先、可能冲突的三路合并)。

```go
import "github.com/rushteam/beauty/pkg/foundation/crdt"

// 可增可减计数器(点赞/库存):多副本并发改,交换状态后收敛
a := crdt.NewPNCounter()
b := crdt.NewPNCounter()
a.Inc("node-a", 10); b.Dec("node-b", 3)

// delta-state 增量同步:只发"自上次以来的变化",带宽远小于全量
b.MergeDelta(a.Delta())
a.MergeDelta(b.Delta())
fmt.Println(a.Value(), b.Value()) // 7 7(收敛)
```

成员:`GCounter`(只增计数)、`PNCounter`(可增可减)、`GSet`(只增集合)、`ORSet`(可增可删,add 优先)、`LWWRegister`(最后写入胜)。

要点:

- 两种同步:全量 `Merge`(反熵兜底)与 **delta-state 增量 `Delta()`/`MergeDelta()`**(gossip 省带宽,结果与全量一致)。
- 每个类型都有 JSON 序列化(跨节点交换状态)。副本标识 nodeID 必须全局唯一。
- 本包是**状态型 CRDT**;本地更新需调用方串行化,Merge 在外层协调。

---

## 9. 分布式 ID 号段 idgen

`pkg/idgen` 的号段模式(Leaf-segment):每次从存储**批量领一段** ID 到内存分发,双缓冲预取使几乎不阻塞。相比雪花算法不依赖时钟、ID 更短且近似递增(对 B+ 树索引友好)。

```go
import "github.com/rushteam/beauty/pkg/idgen"

seg := idgen.NewSegment(store, "order") // store 实现 SegmentStore.NextRange
id, _ := seg.Next()                      // 内存自增,段用尽才回存储领下一段
```

要点:

- `SegmentStore` 是存储接口,内存实现 `MemorySegmentStore` 可本地测试;生产用 DB 版见 [`contrib/gorm`](../contrib/gorm) 的 `SegmentStore`(一条 `UPDATE max_id=max_id+step` 原子领段)。
- 双缓冲 + `WithPreloadThreshold`:消耗到阈值时后台预取下一段,削峰避免领段毛刺。
- ID 近似递增(非严格连续):宕机/重启会跳过未用完的段,换来高可用与低锁争用。
