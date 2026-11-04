package jobqueue

import "sort"

// Share 是比例调度中的一档。
// Priority 对应 Job.Priority,Count 是每个周期从这一档放行的任务数。
type Share struct {
	Priority int
	Count    int
}

// WithRatio 按份额轮转各优先级,而不是让数值更小的优先级一直排在前面。
// 每个周期按 shares 的声明顺序放行:某一档没有等待任务时跳过,不空等。
// 同一优先级内部仍按到达顺序。重复的 Priority 保留先声明的那一档;Count < 1 按 1 处理。
// 没有出现在 shares 里的优先级,排在已声明档位之后,每个周期放行 1 个,数值小的在前。
//
// 例:每 5 个普通任务(Priority 10)插 1 个 VIP(Priority 0):
//
//	jobqueue.WithRatio(
//	    jobqueue.Share{Priority: 10, Count: 5},
//	    jobqueue.Share{Priority: 0, Count: 1},
//	)
func WithRatio(shares ...Share) Option {
	return func(c *Config) {
		c.shares = normalizeShares(shares)
	}
}

func normalizeShares(in []Share) []Share {
	seen := make(map[int]struct{}, len(in))
	out := make([]Share, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s.Priority]; ok {
			continue
		}
		seen[s.Priority] = struct{}{}
		if s.Count < 1 {
			s.Count = 1
		}
		out = append(out, s)
	}
	return out
}

// lane 是同一优先级的先来后到队列。head 之前的槽位已出队,并会在积压时压缩。
type lane struct {
	buf  []*Job
	head int
}

func (l *lane) len() int { return len(l.buf) - l.head }

func (l *lane) push(job *Job) { l.buf = append(l.buf, job) }

func (l *lane) popFront() *Job {
	job := l.buf[l.head]
	l.buf[l.head] = nil
	l.head++
	if l.head == len(l.buf) {
		l.buf = nil
		l.head = 0
		return job
	}
	if l.head > 32 && l.head*2 >= len(l.buf) {
		rest := make([]*Job, len(l.buf)-l.head)
		copy(rest, l.buf[l.head:])
		l.buf = rest
		l.head = 0
	}
	return job
}

// readyQueue 按优先级分车道。shares 为空时严格优先级(数值小的整队先走);
// 非空时按份额轮转。
type readyQueue struct {
	lanes    map[int]*lane
	shares   []Share
	shareSet map[int]struct{}

	curSet  bool
	curPri  int
	curLeft int
}

func newReady(shares []Share) *readyQueue {
	copied := make([]Share, len(shares))
	copy(copied, shares)
	set := make(map[int]struct{}, len(copied))
	for _, s := range copied {
		set[s.Priority] = struct{}{}
	}
	return &readyQueue{
		lanes:    map[int]*lane{},
		shares:   copied,
		shareSet: set,
	}
}

func (r *readyQueue) push(job *Job) {
	ln := r.lanes[job.Priority]
	if ln == nil {
		ln = &lane{}
		r.lanes[job.Priority] = ln
	}
	ln.push(job)
}

func (r *readyQueue) len() int {
	n := 0
	for _, ln := range r.lanes {
		n += ln.len()
	}
	return n
}

func (r *readyQueue) pop() (*Job, bool) {
	if len(r.shares) == 0 {
		return r.popStrict()
	}
	return r.popRatio()
}

// popWaiting 取出该优先级队头第一个仍在等待的任务。已取消的直接丢掉,不占份额。
func (r *readyQueue) popWaiting(pri int) *Job {
	ln := r.lanes[pri]
	if ln == nil {
		return nil
	}
	for ln.len() > 0 {
		job := ln.popFront()
		if job.State == StateWaiting {
			if ln.len() == 0 {
				delete(r.lanes, pri)
			}
			return job
		}
	}
	delete(r.lanes, pri)
	return nil
}

func (r *readyQueue) popStrict() (*Job, bool) {
	for {
		pri, ok := r.minPriority()
		if !ok {
			return nil, false
		}
		job := r.popWaiting(pri)
		if job != nil {
			return job, true
		}
	}
}

func (r *readyQueue) minPriority() (int, bool) {
	found := false
	best := 0
	for pri, ln := range r.lanes {
		if ln.len() == 0 {
			continue
		}
		if !found || pri < best {
			best = pri
			found = true
		}
	}
	return best, found
}

func (r *readyQueue) popRatio() (*Job, bool) {
	cycle := r.cycle()
	if len(cycle) == 0 {
		return nil, false
	}
	start := 0
	if r.curSet {
		for i, s := range cycle {
			if s.Priority == r.curPri {
				start = i
				break
			}
		}
	}
	for attempt := 0; attempt < len(cycle); attempt++ {
		idx := (start + attempt) % len(cycle)
		s := cycle[idx]
		job := r.popWaiting(s.Priority)
		if job == nil {
			continue
		}
		left := s.Count
		if r.curSet && s.Priority == r.curPri && r.curLeft > 0 {
			left = r.curLeft
		}
		left--
		if left <= 0 {
			next := cycle[(idx+1)%len(cycle)]
			r.curPri = next.Priority
			r.curSet = true
			r.curLeft = next.Count
		} else {
			r.curPri = s.Priority
			r.curSet = true
			r.curLeft = left
		}
		return job, true
	}
	return nil, false
}

// cycle 是本轮的放行顺序:已声明的档位在前,未声明且当前有任务的优先级按数值从小到大接在后面。
func (r *readyQueue) cycle() []Share {
	out := make([]Share, len(r.shares))
	copy(out, r.shares)
	extras := make([]int, 0)
	for pri, ln := range r.lanes {
		if _, ok := r.shareSet[pri]; ok || ln.len() == 0 {
			continue
		}
		extras = append(extras, pri)
	}
	sort.Ints(extras)
	for _, pri := range extras {
		out = append(out, Share{Priority: pri, Count: 1})
	}
	return out
}
