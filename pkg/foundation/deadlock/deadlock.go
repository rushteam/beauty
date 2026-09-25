// Package deadlock 通过分析 goroutine 栈转储,定位疑似死锁与 goroutine 泄漏。
//
// 运行中的进程若出现 goroutine 长时间阻塞(chan send/receive、mutex、IO wait 等),
// 往往是死锁或泄漏的信号。Go runtime 的 goroutine dump(pprof debug=2)会为阻塞
// 超过 1 分钟的 goroutine 标注 "[state, N minutes]"。本包抓取该 dump,按调用栈
// 聚合(忽略栈帧中因地址/偏移不同带来的差异),并按最长阻塞时长、数量排序,
// 让"卡得最久、数量最多"的可疑调用栈浮到最前。
//
// 用法:
//
//	rep, err := deadlock.Detect(deadlock.WithMinWaitMinutes(1))
//	if err != nil { ... }
//	fmt.Println(rep) // 文本报告
//
// 也可分析已有 dump(便于测试与离线分析):
//
//	rep, _ := deadlock.Analyze(dump, deadlock.WithIgnore("internal/poll.runtime_pollWait("))
//
// 本包只做只读采样与解析,不会修改运行时状态,可安全用于生产排查。
package deadlock

import (
	"bytes"
	"fmt"
	"regexp"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
)

var (
	// headerRe 匹配 goroutine 块首行:
	//   goroutine 12 [chan receive, 5 minutes]:
	//   goroutine 1 [running]:
	headerRe = regexp.MustCompile(`^goroutine\s+(\d+)\s+\[([^,\]]+)(?:,\s+(\d+)\s+minutes)?\]:`)
	// addressRe 用于把栈帧里的十六进制地址/偏移归一化,使相同调用栈能聚合到一起。
	addressRe = regexp.MustCompile(`0x[0-9a-fA-F]+`)
)

// Group 表示一组调用栈相同的 goroutine。
type Group struct {
	Count    int      // 该调用栈的 goroutine 数量
	WaitMins int      // 该组中最长阻塞时长(分钟);0 表示未标注(阻塞 < 1 分钟)
	State    string   // goroutine 状态,如 "chan receive"、"IO wait"、"semacquire"
	Header   string   // 代表性首行(取阻塞最久的那条)
	Frames   []string // 代表性调用栈(不含首行 header)
}

// Report 是一次 goroutine dump 的分析结果。
type Report struct {
	Total  int     // dump 中成功解析的 goroutine 总数
	Groups []Group // 通过过滤条件的分组,按可疑度排序(阻塞时长 > 数量 > header)
}

type options struct {
	minWaitMinutes int
	ignore         []string
}

// Option 配置分析行为。
type Option func(*options)

// WithMinWaitMinutes 只报告最长阻塞时长 >= n 分钟的组。默认 1,
// 即只关注被 runtime 标注了 minutes(阻塞超过 1 分钟)的 goroutine。
// 传 0 可报告全部 goroutine 分组(包含正在运行/短暂阻塞的)。
func WithMinWaitMinutes(n int) Option {
	return func(o *options) { o.minWaitMinutes = n }
}

// WithIgnore 忽略"栈顶函数行"以任一前缀开头的 goroutine,
// 常用于过滤正常阻塞,例如:
//
//	deadlock.WithIgnore("internal/poll.runtime_pollWait(") // 网络 IO 轮询
func WithIgnore(prefixes ...string) Option {
	return func(o *options) { o.ignore = append(o.ignore, prefixes...) }
}

// Detect 抓取当前进程的 goroutine dump 并分析。
func Detect(opts ...Option) (*Report, error) {
	p := pprof.Lookup("goroutine")
	if p == nil {
		return nil, fmt.Errorf("deadlock: pprof goroutine profile 不可用")
	}
	var buf bytes.Buffer
	buf.Grow(1 << 16)
	if err := p.WriteTo(&buf, 2); err != nil {
		return nil, fmt.Errorf("deadlock: 抓取 goroutine dump 失败: %w", err)
	}
	return Analyze(buf.Bytes(), opts...)
}

type parsed struct {
	state    string
	waitMins int
	header   string
	frames   []string
}

// Analyze 解析给定的 goroutine dump(pprof debug=2 格式)并生成报告。
func Analyze(dump []byte, opts ...Option) (*Report, error) {
	o := options{minWaitMinutes: 1}
	for _, fn := range opts {
		fn(&o)
	}

	type agg struct {
		count    int
		waitMins int
		state    string
		header   string
		frames   []string
	}
	groups := make(map[string]*agg)
	order := make([]string, 0, 16) // 保持首次出现顺序,排序前更稳定

	rep := &Report{}
	// goroutine 块之间以空行分隔。
	for _, block := range bytes.Split(dump, []byte("\n\n")) {
		g, ok := parseBlock(block)
		if !ok {
			continue
		}
		rep.Total++

		if o.minWaitMinutes > 0 && g.waitMins < o.minWaitMinutes {
			continue
		}
		if isIgnored(o.ignore, g.frames) {
			continue
		}

		key := normalizeKey(g.frames)
		a, exists := groups[key]
		if !exists {
			a = &agg{state: g.state, header: g.header, frames: g.frames, waitMins: g.waitMins}
			groups[key] = a
			order = append(order, key)
			a.count++
			continue
		}
		a.count++
		// 用阻塞最久的那条作为该组的代表。
		if g.waitMins >= a.waitMins {
			a.waitMins = g.waitMins
			a.header = g.header
			a.frames = g.frames
			a.state = g.state
		}
	}

	for _, key := range order {
		a := groups[key]
		rep.Groups = append(rep.Groups, Group{
			Count:    a.count,
			WaitMins: a.waitMins,
			State:    a.state,
			Header:   a.header,
			Frames:   a.frames,
		})
	}
	sort.SliceStable(rep.Groups, func(i, j int) bool {
		gi, gj := rep.Groups[i], rep.Groups[j]
		if gi.WaitMins != gj.WaitMins {
			return gi.WaitMins > gj.WaitMins
		}
		if gi.Count != gj.Count {
			return gi.Count > gj.Count
		}
		return gi.Header < gj.Header
	})
	return rep, nil
}

// parseBlock 解析单个 goroutine 块。返回 false 表示该块不是有效的 goroutine 记录。
func parseBlock(block []byte) (parsed, bool) {
	var g parsed
	lines := strings.Split(strings.TrimRight(string(block), "\n"), "\n")

	i := 0
	for ; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "goroutine ") {
			break
		}
	}
	if i >= len(lines) {
		return g, false
	}
	m := headerRe.FindStringSubmatch(lines[i])
	if m == nil {
		return g, false
	}
	g.header = lines[i]
	g.state = strings.TrimSpace(m[2])
	if m[3] != "" {
		g.waitMins, _ = strconv.Atoi(m[3])
	}
	for _, ln := range lines[i+1:] {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		g.frames = append(g.frames, ln)
	}
	return g, true
}

// isIgnored 判断栈顶函数行是否命中忽略前缀。
func isIgnored(prefixes []string, frames []string) bool {
	if len(prefixes) == 0 || len(frames) == 0 {
		return false
	}
	top := frames[0]
	for _, p := range prefixes {
		if strings.HasPrefix(top, p) {
			return true
		}
	}
	return false
}

// normalizeKey 归一化调用栈,作为聚合 key:去掉不同 goroutine 间必然不同的
// 十六进制地址与偏移,使"相同代码路径"的 goroutine 聚到一起。
func normalizeKey(frames []string) string {
	return addressRe.ReplaceAllString(strings.Join(frames, "\n"), "0x?")
}

// String 以文本形式渲染报告,适合直接打印或在控制台 <pre> 中展示。
func (r *Report) String() string {
	if r == nil || len(r.Groups) == 0 {
		total := 0
		if r != nil {
			total = r.Total
		}
		return fmt.Sprintf("未发现可疑 goroutine (total=%d)", total)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "goroutine 总数=%d, 可疑分组=%d\n", r.Total, len(r.Groups))
	for i, g := range r.Groups {
		fmt.Fprintf(&b, "\n#%d  count=%d  waitMins=%d  state=%s\n", i+1, g.Count, g.WaitMins, g.State)
		b.WriteString(g.Header)
		b.WriteByte('\n')
		for _, f := range g.Frames {
			b.WriteString(f)
			b.WriteByte('\n')
		}
	}
	return b.String()
}
