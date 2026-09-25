package diff

import (
	"fmt"
	"strconv"
)

// itoa 整数转十进制字符串。
func itoa(n int) string { return strconv.Itoa(n) }

// sprint 把任意元素渲染为字符串:字符串原样输出,其余走 fmt.Sprint。
func sprint(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}
