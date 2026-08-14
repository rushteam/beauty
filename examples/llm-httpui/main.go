// llm-httpui demo:contrib/llm/agent/httpui SSE + checkpoint 回放。
//
// POST /run → RunStream SSE;GET /events?run_id= → 回放。
//
// 运行:go run ./examples/llm-httpui
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
	"github.com/rushteam/beauty/contrib/llm/agent/httpui"
	"github.com/rushteam/beauty/pkg/service/webserver"
)

const addr = "127.0.0.1:8299"

func main() {
	store := agent.NewMemoryCheckpointStore()
	h := &httpui.Handler{Agent: demoAgent{name: "httpui-demo"}, Name: "httpui-demo", Store: store}
	mux := http.NewServeMux()
	mux.Handle("/", h)

	app := beauty.New(beauty.WithWebServer(addr, mux, webserver.WithServiceName("llm-httpui")))

	ctx, cancel := context.WithCancel(context.Background())
	appErr := make(chan error, 1)
	go func() { appErr <- app.Start(ctx) }()
	time.Sleep(80 * time.Millisecond)

	ok, runID := selfTest()
	fmt.Println("──────── llm-httpui 自测 ────────")
	if ok {
		fmt.Printf("结论: ✅ SSE run + replay(run_id=%s)\n", runID)
	} else {
		fmt.Println("结论: ❌ 自测失败")
	}

	cancel()
	<-appErr
	if !ok {
		panic("self test failed")
	}
}

type demoAgent struct{ name string }

func (d demoAgent) Info() agent.Info { return agent.Info{Name: d.name} }

func (d demoAgent) Run(ctx context.Context, req llm.Request) agent.RunOutcome {
	return agent.RunOutcome{Status: agent.StatusDone, RunID: "run-demo", Response: &llm.Response{Content: "ok"}}
}

func (d demoAgent) Continue(ctx context.Context, runID string, resolutions []agent.Resolution) agent.RunOutcome {
	return d.Run(ctx, llm.Request{})
}

func (d demoAgent) RunStream(ctx context.Context, req llm.Request) <-chan agent.Event {
	ch := make(chan agent.Event, 2)
	go func() {
		defer close(ch)
		ch <- agent.Event{Type: agent.EventStep, RunID: "run-demo", Response: &llm.Response{Content: "thinking"}}
		ch <- agent.Event{Type: agent.EventFinal, RunID: "run-demo", Response: &llm.Response{Content: "done"}}
	}()
	return ch
}

func (d demoAgent) ContinueStream(ctx context.Context, runID string, resolutions []agent.Resolution) <-chan agent.Event {
	return d.RunStream(ctx, llm.Request{})
}

func selfTest() (bool, string) {
	body := `{"model":"demo","messages":[{"role":"user","content":"hi"}]}`
	resp, err := http.Post("http://"+addr+"/run", "application/json", strings.NewReader(body))
	if err != nil {
		return false, ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, ""
	}
	runID, lines := readSSEDataLines(resp.Body)
	if lines < 2 || runID == "" {
		return false, runID
	}

	replay, err := http.Get("http://" + addr + "/events?run_id=run-demo")
	if err != nil {
		return false, runID
	}
	defer replay.Body.Close()
	if replay.StatusCode != http.StatusOK {
		return false, runID
	}
	_, replayLines := readSSEDataLines(replay.Body)
	return replayLines >= 1, "run-demo"
}

func readSSEDataLines(r io.Reader) (runID string, dataLines int) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		dataLines++
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		var frame struct {
			RunID string `json:"run_id"`
		}
		if json.Unmarshal([]byte(payload), &frame) == nil && frame.RunID != "" {
			runID = frame.RunID
		}
	}
	return runID, dataLines
}
