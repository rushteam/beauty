# 基础数据结构:过滤器 / 跳表 / 差异

`pkg/foundation` 下的三个纯标准库、零依赖数据结构包,解决"判存去重、有序索引、差异对比"三类通用问题。

- 概率过滤器:[`pkg/foundation/filter`](../pkg/foundation/filter) — 示例 [filter](../examples/filter)
- 有序跳表:[`pkg/foundation/skiplist`](../pkg/foundation/skiplist) — 示例 [skiplist](../examples/skiplist)
- 差异/补丁:[`pkg/foundation/diff`](../pkg/foundation/diff) — 示例 [diff](../examples/diff)

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
- 算法为 LCS 动态规划(O(n·m)),适合配置/文档规模;超大输入或追求最短编辑距离可换 Myers。
- 无状态、纯函数,天然并发安全。
