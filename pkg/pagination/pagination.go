// Package pagination 提供 keyset(游标)分页原语:把"下一页起点"编码成一个**不透明**的、URL 安全的
// 游标字符串,客户端原样回传即可翻页。相较 offset 分页,keyset 分页在深翻页时依然高效、结果稳定
// (不受插入/删除位移影响)。
//
// 游标 = base64url(json(游标值))。游标值通常是"上一页最后一行的排序键 + 唯一 ID",由使用方定义结构。
// 注意:游标只是"不透明",并非防篡改;若不信任客户端(游标里含敏感/可越权字段),请另加 HMAC 签名。
//
// 典型用法:
//
//	type Cur struct{ CreatedAt int64 `json:"c"`; ID int64 `json:"i"` }
//	// 查询:WHERE (created_at,id) < (cur.CreatedAt,cur.ID) ORDER BY created_at DESC,id DESC LIMIT n
//	next, _ := pagination.Encode(Cur{last.CreatedAt, last.ID})
//	// 下次请求带上 next,Decode 回结构继续查。
package pagination

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// Encode 把游标值编码为不透明字符串(base64url(json))。
func Encode[T any](v T) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("pagination: 编码游标: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Decode 把游标字符串解回游标值。空串返回类型零值 + false(表示"从头开始",非错误)。
// 格式非法则返回错误。
func Decode[T any](cursor string) (v T, ok bool, err error) {
	if cursor == "" {
		return v, false, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return v, false, fmt.Errorf("pagination: 游标 base64 非法: %w", err)
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return v, false, fmt.Errorf("pagination: 游标 json 非法: %w", err)
	}
	return v, true, nil
}

// Page 是一页结果:Items 为本页数据,Next 为下一页游标(为空表示没有下一页)。
type Page[Item any] struct {
	Items []Item `json:"items"`
	Next  string `json:"next,omitempty"`
}

// Build 组装一页结果:传入"多取一条"的切片(len 最多 limit+1)与如何从某条生成游标的函数。
// 若长度 > limit,说明还有下一页——截断到 limit 条,并用第 limit 条(最后一条)生成 Next 游标;
// 否则 Next 为空。这是 keyset 分页的常见收尾:查询时 LIMIT limit+1,用多出来的那条判断是否还有更多。
func Build[Item any, Cur any](rows []Item, limit int, cursorOf func(Item) Cur) (Page[Item], error) {
	if limit <= 0 || len(rows) <= limit {
		return Page[Item]{Items: rows}, nil
	}
	items := rows[:limit]
	next, err := Encode(cursorOf(items[limit-1]))
	if err != nil {
		return Page[Item]{}, err
	}
	return Page[Item]{Items: items, Next: next}, nil
}
