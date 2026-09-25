package deadlock

import (
	"strings"
	"testing"
)

// 构造一段贴近真实 pprof debug=2 输出的 dump 供解析测试使用。
const sampleDump = `goroutine 1 [chan receive, 10 minutes]:
main.wait(0xc0000a1000)
	/app/main.go:20 +0x1c
main.main()
	/app/main.go:10 +0x40

goroutine 2 [chan receive, 8 minutes]:
main.wait(0xc0000a2000)
	/app/main.go:20 +0x9f
main.main()
	/app/main.go:10 +0xab

goroutine 3 [semacquire, 3 minutes]:
sync.runtime_Semacquire(0xc0000b0000)
	/usr/local/go/src/runtime/sema.go:62 +0x25
main.lock()
	/app/lock.go:30 +0x55

goroutine 4 [IO wait]:
internal/poll.runtime_pollWait(0x7f00, 0x72)
	/usr/local/go/src/runtime/netpoll.go:343 +0x85
net.(*netFD).Read(0xc000100000)
	/usr/local/go/src/net/fd_posix.go:55 +0x25

goroutine 5 [running]:
runtime/pprof.writeGoroutineStacks({0x10, 0xc000})
	/usr/local/go/src/runtime/pprof/pprof.go:770 +0x6e
`

func TestAnalyze(t *testing.T) {
	tests := []struct {
		name       string
		opts       []Option
		wantTotal  int
		wantGroups int
		check      func(t *testing.T, r *Report)
	}{
		{
			name:       "default only reports goroutines blocked >= 1 min",
			opts:       nil,
			wantTotal:  5,
			wantGroups: 2, // 两个 chan receive 聚合为 1 组 + 1 个 semacquire 组; IO wait/running 无 minutes 被过滤
			check: func(t *testing.T, r *Report) {
				// 阻塞最久(10 分钟)的 chan receive 组应排第一,且聚合了 2 个 goroutine。
				first := r.Groups[0]
				if first.Count != 2 {
					t.Errorf("first group count = %d, want 2", first.Count)
				}
				if first.WaitMins != 10 {
					t.Errorf("first group waitMins = %d, want 10", first.WaitMins)
				}
				if first.State != "chan receive" {
					t.Errorf("first group state = %q, want chan receive", first.State)
				}
			},
		},
		{
			name:       "min wait 5 minutes filters out semacquire(3m)",
			opts:       []Option{WithMinWaitMinutes(5)},
			wantTotal:  5,
			wantGroups: 1,
		},
		{
			name:       "min wait 0 reports every goroutine",
			opts:       []Option{WithMinWaitMinutes(0)},
			wantTotal:  5,
			wantGroups: 4, // chan receive x2 聚合, 其余各 1 组 => 4
		},
		{
			name:       "ignore prefix drops matching top frame",
			opts:       []Option{WithMinWaitMinutes(0), WithIgnore("runtime/pprof.writeGoroutineStacks")},
			wantTotal:  5,
			wantGroups: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := Analyze([]byte(sampleDump), tt.opts...)
			if err != nil {
				t.Fatalf("Analyze() error = %v", err)
			}
			if r.Total != tt.wantTotal {
				t.Errorf("Total = %d, want %d", r.Total, tt.wantTotal)
			}
			if len(r.Groups) != tt.wantGroups {
				t.Errorf("len(Groups) = %d, want %d", len(r.Groups), tt.wantGroups)
			}
			if tt.check != nil {
				tt.check(t, r)
			}
		})
	}
}

func TestAnalyzeGroupsSortedByWaitThenCount(t *testing.T) {
	r, err := Analyze([]byte(sampleDump))
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(r.Groups); i++ {
		if r.Groups[i-1].WaitMins < r.Groups[i].WaitMins {
			t.Fatalf("groups not sorted by waitMins desc: %+v", r.Groups)
		}
	}
}

func TestReportString(t *testing.T) {
	var empty *Report
	if got := empty.String(); !strings.Contains(got, "未发现可疑 goroutine") {
		t.Errorf("nil report String() = %q", got)
	}

	r, _ := Analyze([]byte(sampleDump))
	s := r.String()
	if !strings.Contains(s, "count=2") || !strings.Contains(s, "waitMins=10") {
		t.Errorf("String() missing summary, got:\n%s", s)
	}
}

func TestDetect(t *testing.T) {
	// Detect 抓取当前进程,至少应能解析出若干 goroutine,不报错。
	r, err := Detect(WithMinWaitMinutes(0))
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if r.Total == 0 {
		t.Errorf("Detect() Total = 0, want > 0")
	}
}
