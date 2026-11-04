package session

import (
	"errors"
	"time"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
)

// ErrBothCursors 表示分页同时给了 Before 和 After。
var ErrBothCursors = errors.New("session: both before and after cursors set")

// Run 是会话里的一次执行记录。ParentID 为空表示顶层 run。
type Run struct {
	Index      int             `json:"index"`
	ID         string          `json:"id"`
	ParentID   string          `json:"parent_id,omitempty"`
	Status     agent.RunStatus `json:"status"`
	Usage      llm.Usage       `json:"usage,omitempty"`
	StartedAt  time.Time       `json:"started_at,omitempty"`
	FinishedAt time.Time       `json:"finished_at,omitempty"`
	// Children 只在 RunsPage 的结果里填充,不写入存储。
	Children []Run `json:"-"`
}

// RunQuery 选择一页顶层 run。Limit <= 0 表示符合条件的全部。
// Before 取更旧的一页(Index 小于该值);After 取更新的一页(Index 大于该值)。
type RunQuery struct {
	Limit  int
	Before *int
	After  *int
	Status []agent.RunStatus
	Since  time.Time
	Until  time.Time
}

// RunPage 是一页顶层 run,按时间从旧到新。子 run 挂在 Children 上,不计入 Total 和 Limit。
type RunPage struct {
	Runs    []Run
	Total   int
	HasMore bool
	Oldest  int
	Newest  int
}

type turnLog struct {
	steps      map[string]llm.Usage
	order      []string
	status     agent.RunStatus
	final      string
	finalUsage llm.Usage
	at         time.Time
}

func (t *turnLog) see(ev agent.Event) {
	if t.steps == nil {
		t.steps = map[string]llm.Usage{}
	}
	if t.at.IsZero() {
		t.at = time.Now()
	}
	if ev.RunID != "" {
		if _, ok := t.steps[ev.RunID]; !ok {
			t.steps[ev.RunID] = llm.Usage{}
			t.order = append(t.order, ev.RunID)
		}
	}
	switch ev.Type {
	case agent.EventStep:
		if ev.Response != nil && ev.RunID != "" {
			t.steps[ev.RunID] = t.steps[ev.RunID].Add(ev.Response.Usage)
		}
	case agent.EventFinal:
		t.status = agent.StatusDone
		t.final = ev.RunID
		if ev.Response != nil {
			t.finalUsage = ev.Response.Usage
		}
	case agent.EventPaused:
		t.status = agent.StatusPaused
		t.final = ev.RunID
		if ev.Response != nil {
			t.finalUsage = ev.Response.Usage
		}
	case agent.EventError:
		t.status = agent.StatusError
		t.final = ev.RunID
	}
}

func (t *turnLog) commit(sess *Session) {
	if t == nil || sess == nil || t.final == "" {
		return
	}
	var sum llm.Usage
	now := time.Now()
	var children []Run
	for _, id := range t.order {
		u := t.steps[id]
		sum = sum.Add(u)
		if id == t.final {
			continue
		}
		children = append(children, Run{
			ID:         id,
			ParentID:   t.final,
			Status:     agent.StatusDone,
			Usage:      u,
			StartedAt:  t.at,
			FinishedAt: now,
		})
	}
	if sum == (llm.Usage{}) {
		sum = t.finalUsage
	}
	sess.upsertRun(Run{
		ID:         t.final,
		Status:     t.status,
		Usage:      sum,
		StartedAt:  t.at,
		FinishedAt: now,
	})
	for _, c := range children {
		sess.upsertRun(c)
	}
}

func (s *Session) upsertRun(r Run) {
	r.Children = nil
	for i := range s.Runs {
		if s.Runs[i].ID != r.ID {
			continue
		}
		prev := s.Runs[i]
		r.Usage = prev.Usage.Add(r.Usage)
		r.Index = prev.Index
		if !prev.StartedAt.IsZero() {
			r.StartedAt = prev.StartedAt
		}
		if r.ParentID == "" {
			r.ParentID = prev.ParentID
		}
		if r.ParentID == "" {
			s.Usage = s.Usage.Sub(prev.Usage).Add(r.Usage)
		}
		s.Runs[i] = r
		return
	}
	r.Index = nextRunIndex(s.Runs)
	s.Runs = append(s.Runs, r)
	if r.ParentID == "" {
		s.Usage = s.Usage.Add(r.Usage)
	}
}

func nextRunIndex(runs []Run) int {
	n := 0
	for _, r := range runs {
		if r.Index >= n {
			n = r.Index + 1
		}
	}
	return n
}

// RunsPage 返回一页顶层 run。子 run 挂在对应顶层的 Children 上。
func (s *Session) RunsPage(q RunQuery) (RunPage, error) {
	if s == nil {
		return RunPage{Oldest: -1, Newest: -1}, nil
	}
	if q.Before != nil && q.After != nil {
		return RunPage{Oldest: -1, Newest: -1}, ErrBothCursors
	}
	var matched []Run
	for _, r := range s.Runs {
		if r.ParentID != "" || !matchRun(r, q) {
			continue
		}
		matched = append(matched, r)
	}
	page := RunPage{Total: len(matched), Oldest: -1, Newest: -1}
	selected := windowRuns(matched, q, &page.HasMore)
	if len(selected) == 0 {
		return page, nil
	}
	page.Oldest = selected[0].Index
	page.Newest = selected[len(selected)-1].Index
	for _, top := range selected {
		top.Children = childTree(s.Runs, top.ID)
		page.Runs = append(page.Runs, top)
	}
	return page, nil
}

func matchRun(r Run, q RunQuery) bool {
	if len(q.Status) > 0 {
		ok := false
		for _, st := range q.Status {
			if r.Status == st {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if !q.Since.IsZero() && r.StartedAt.Before(q.Since) {
		return false
	}
	if !q.Until.IsZero() && r.StartedAt.After(q.Until) {
		return false
	}
	return true
}

func windowRuns(matched []Run, q RunQuery, hasMore *bool) []Run {
	switch {
	case q.After != nil:
		var newer []Run
		for _, r := range matched {
			if r.Index > *q.After {
				newer = append(newer, r)
			}
		}
		if q.Limit > 0 && len(newer) > q.Limit {
			*hasMore = true
			return newer[:q.Limit]
		}
		return newer
	case q.Before != nil:
		var older []Run
		for _, r := range matched {
			if r.Index < *q.Before {
				older = append(older, r)
			}
		}
		if q.Limit > 0 && len(older) > q.Limit {
			*hasMore = true
			return older[len(older)-q.Limit:]
		}
		return older
	default:
		if q.Limit > 0 && len(matched) > q.Limit {
			*hasMore = true
			return matched[len(matched)-q.Limit:]
		}
		return matched
	}
}

func childTree(all []Run, parentID string) []Run {
	var out []Run
	for _, r := range all {
		if r.ParentID != parentID {
			continue
		}
		r.Children = childTree(all, r.ID)
		out = append(out, r)
	}
	return out
}
