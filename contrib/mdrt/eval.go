package mdrt

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rushteam/beauty/contrib/llm"
)

// EvalReport 是一次 Build 评测的汇总。
type EvalReport struct {
	Model        string `json:"model"`
	Files        int    `json:"files"`
	OK           bool   `json:"ok"`
	Error        string `json:"error,omitempty"`
	LLMCalls     uint64 `json:"llmCalls"`
	InputTokens  uint64 `json:"inputTokens"`
	OutputTokens uint64 `json:"outputTokens"`
	Repairs      uint64 `json:"repairs"`
	CacheHits    uint64 `json:"cacheHits"`
}

// Eval 用给定模型编译整个 spec 目录,把报告写到 stateDir/eval/<时间>.json。
// 验收失败时报告里带错误,同时返回该错误。
func Eval(ctx context.Context, specDir, stateDir string, client llm.Client, model string) (*EvalReport, error) {
	rt, err := Build(ctx,
		WithSpecDir(specDir),
		WithStateDir(stateDir),
		WithLockFile(filepath.Join(stateDir, "lock.json")),
		WithLLM(client, model),
		WithModel(model),
	)
	report := &EvalReport{Model: model}
	if rt != nil {
		st := rt.Stats()
		report.Files = len(rt.Status())
		report.LLMCalls = st.LLMCalls
		report.InputTokens = st.InputTokens
		report.OutputTokens = st.OutputTokens
		report.Repairs = st.Repairs
		report.CacheHits = st.CacheHits
	}
	if err != nil {
		report.Error = err.Error()
	} else {
		report.OK = true
	}
	if stateDir != "" {
		dir := filepath.Join(stateDir, "eval")
		_ = os.MkdirAll(dir, 0o755)
		raw, mErr := json.MarshalIndent(report, "", "  ")
		if mErr == nil {
			name := fmt.Sprintf("%s.json", time.Now().UTC().Format("20060102T150405.000000000"))
			_ = writeAtomic(filepath.Join(dir, name), append(raw, '\n'))
		}
	}
	return report, err
}
