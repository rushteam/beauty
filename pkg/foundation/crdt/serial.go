package crdt

import "encoding/json"

// 本文件给各 CRDT 加 JSON 序列化,这是跨节点同步的前提:副本把自身状态 Marshal 成字节流发给
// 对端,对端 Unmarshal 到一个 CRDT 再 Merge 进本地状态,即完成一次状态交换(反熵/gossip)。
//
// 泛型集合/寄存器把元素编码为数组(而非 map 的 key),故元素类型 T 只要能被 encoding/json
// 编解码即可(string/数值/结构体……),不受 "map key 必须是字符串" 的限制。

// ----- GCounter -----

type gcJSON struct {
	Counts map[string]uint64 `json:"counts"`
}

// MarshalJSON 实现 json.Marshaler。
func (c *GCounter) MarshalJSON() ([]byte, error) {
	return json.Marshal(gcJSON{Counts: c.counts})
}

// UnmarshalJSON 实现 json.Unmarshaler。
func (c *GCounter) UnmarshalJSON(b []byte) error {
	var w gcJSON
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	if w.Counts == nil {
		w.Counts = map[string]uint64{}
	}
	c.counts = w.Counts
	return nil
}

// ----- PNCounter -----

type pnJSON struct {
	P *GCounter `json:"p"`
	N *GCounter `json:"n"`
}

func (c *PNCounter) MarshalJSON() ([]byte, error) {
	return json.Marshal(pnJSON{P: c.p, N: c.n})
}

func (c *PNCounter) UnmarshalJSON(b []byte) error {
	w := pnJSON{P: NewGCounter(), N: NewGCounter()}
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	c.p, c.n = w.P, w.N
	return nil
}

// ----- GSet -----

type gsetJSON[T comparable] struct {
	Elements []T `json:"elements"`
}

func (s *GSet[T]) MarshalJSON() ([]byte, error) {
	return json.Marshal(gsetJSON[T]{Elements: s.Elements()})
}

func (s *GSet[T]) UnmarshalJSON(b []byte) error {
	var w gsetJSON[T]
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	s.m = make(map[T]struct{}, len(w.Elements))
	for _, e := range w.Elements {
		s.m[e] = struct{}{}
	}
	return nil
}

// ----- ORSet -----

type orEntry[T comparable] struct {
	Elem T        `json:"elem"`
	Tags []string `json:"tags"`
}

type orsetJSON[T comparable] struct {
	Node string       `json:"node"`
	Seq  uint64       `json:"seq"`
	Adds []orEntry[T] `json:"adds"`
	Tomb []string     `json:"tomb"`
}

func (s *ORSet[T]) MarshalJSON() ([]byte, error) {
	w := orsetJSON[T]{Node: s.node, Seq: s.seq}
	for e, tags := range s.adds {
		ts := make([]string, 0, len(tags))
		for t := range tags {
			ts = append(ts, t)
		}
		w.Adds = append(w.Adds, orEntry[T]{Elem: e, Tags: ts})
	}
	for t := range s.tomb {
		w.Tomb = append(w.Tomb, t)
	}
	return json.Marshal(w)
}

func (s *ORSet[T]) UnmarshalJSON(b []byte) error {
	var w orsetJSON[T]
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	s.node = w.Node
	s.seq = w.Seq
	s.adds = make(map[T]map[string]struct{}, len(w.Adds))
	for _, e := range w.Adds {
		m := make(map[string]struct{}, len(e.Tags))
		for _, t := range e.Tags {
			m[t] = struct{}{}
		}
		s.adds[e.Elem] = m
	}
	s.tomb = make(map[string]struct{}, len(w.Tomb))
	for _, t := range w.Tomb {
		s.tomb[t] = struct{}{}
	}
	return nil
}

// ----- LWWRegister -----

type lwwJSON[T any] struct {
	Val  T      `json:"val"`
	TS   int64  `json:"ts"`
	Node string `json:"node"`
	Set  bool   `json:"set"`
}

func (r *LWWRegister[T]) MarshalJSON() ([]byte, error) {
	return json.Marshal(lwwJSON[T]{Val: r.val, TS: r.ts, Node: r.node, Set: r.set})
}

func (r *LWWRegister[T]) UnmarshalJSON(b []byte) error {
	var w lwwJSON[T]
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	r.val, r.ts, r.node, r.set = w.Val, w.TS, w.Node, w.Set
	return nil
}
