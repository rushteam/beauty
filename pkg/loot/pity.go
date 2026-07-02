package loot

// Puller 是带保底(pity)状态的抽取器:在 Table 之上记录"连续多少次没抽到
// >= 目标稀有度的物品",达到 pity 阈值时,强制从"高稀有度子表"抽一个。
//
// 典型:抽卡保底——连续 89 抽没出 5 星,第 90 抽必出 5 星。
//
// Puller 有内部计数状态,非并发安全:每个玩家一个 Puller(天然隔离),
// 或调用方自行加锁。零值不可用,用 NewPuller 构造。
type Puller[T any] struct {
	table     *Table[T]
	highTable *Table[T] // 仅含 Rarity>=pityRarity 的物品(保底触发时从这里抽)
	pityLimit int       // 连续多少次未出目标稀有度则触发保底(<=0 关闭保底)
	pityRare  int       // 目标稀有度阈值:抽到 Rarity>=该值即视为"出货",重置计数
	miss      int       // 当前连续未出货次数
}

// NewPuller 创建带保底的抽取器。
// pityLimit:连续未出货达此次数时,下一抽强制出货(<=0 关闭保底,等价裸 Table)。
// pityRarity:稀有度阈值,抽到 Rarity>=它视为出货。
//
// 若表内没有任何 Rarity>=pityRarity 的物品,保底无法触发(退化为普通抽取)。
func NewPuller[T any](table *Table[T], pityLimit, pityRarity int) *Puller[T] {
	p := &Puller[T]{
		table:     table,
		pityLimit: pityLimit,
		pityRare:  pityRarity,
	}
	if pityLimit > 0 {
		// 预构建高稀有度子表(保底触发时按权重从中抽)。
		var high []Item[T]
		for _, it := range table.items {
			if it.Rarity >= pityRarity {
				high = append(high, it)
			}
		}
		if len(high) > 0 {
			p.highTable, _ = NewTable(high, WithRand[T](table.rng))
		}
	}
	return p
}

// Draw 抽一次并维护保底计数:
//   - 若已连续未出货达 pityLimit 且存在高稀有度子表,强制出货(从高稀有度子表抽),计数清零;
//   - 否则正常抽;抽到 Rarity>=pityRarity 视为出货,计数清零;未出货则计数 +1。
//
// 返回抽到的 Item 与本次是否由保底触发(pity)。
func (p *Puller[T]) Draw() (Item[T], bool) {
	// 保底触发。
	if p.pityLimit > 0 && p.highTable != nil && p.miss >= p.pityLimit {
		it := p.highTable.DrawItem()
		p.miss = 0
		return it, true
	}
	it := p.table.DrawItem()
	if p.pityLimit > 0 && it.Rarity >= p.pityRare {
		p.miss = 0 // 自然出货,重置
	} else if p.pityLimit > 0 {
		p.miss++
	}
	return it, false
}

// DrawN 连抽 n 次(每次都维护保底计数)。返回抽到的物品及各次是否保底触发。
func (p *Puller[T]) DrawN(n int) (items []Item[T], pity []bool) {
	if n <= 0 {
		return nil, nil
	}
	items = make([]Item[T], n)
	pity = make([]bool, n)
	for i := range n {
		items[i], pity[i] = p.Draw()
	}
	return items, pity
}

// Misses 返回当前连续未出货次数(距离保底还差 pityLimit-Misses 抽)。
func (p *Puller[T]) Misses() int { return p.miss }

// Reset 清零保底计数。
func (p *Puller[T]) Reset() { p.miss = 0 }
