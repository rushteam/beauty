package agent_test

import (
	"iter"

	"github.com/rushteam/beauty/contrib/llm"
)

// unusedStream 返回空的 iter 流(测试用 fakeClient 默认实现)。
func unusedStream() iter.Seq2[llm.Chunk, error] {
	return func(yield func(llm.Chunk, error) bool) {}
}
