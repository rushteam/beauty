package discover

import (
	"bytes"
	"encoding/json"
	"maps"
)

type Service interface {
	ID() string
	Name() string
	Kind() string
	Addr() string
	Metadata() map[string]string
}

type ServiceInfo struct {
	ID       string            `json:"id"`
	Kind     string            `json:"kind"`
	Name     string            `json:"name"`
	Addr     string            `json:"addr"`
	Metadata map[string]string `json:"metadata"`
}

func (s *ServiceInfo) Unmarshal(b []byte) error {
	buf := bytes.NewBuffer(b)
	decoder := json.NewDecoder(buf)
	decoder.UseNumber()
	return decoder.Decode(s)
}

// Equal 报告两个实例的全部字段（含 Metadata）是否相同，
// 可配合 slices.EqualFunc(a, b, ServiceInfo.Equal) 判断推送是否真的有变化。
func (s ServiceInfo) Equal(o ServiceInfo) bool {
	return s.ID == o.ID && s.Kind == o.Kind && s.Name == o.Name && s.Addr == o.Addr &&
		maps.Equal(s.Metadata, o.Metadata)
}

func (s *ServiceInfo) Marshal() (string, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
