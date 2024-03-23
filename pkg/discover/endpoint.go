package discover

import (
	"bytes"
	"encoding/json"
)

type Service interface {
	ID() string
	Name() string
	Addr() string
	Metadata() map[string]string
}

type ServiceInfo struct {
	Name string `json:"name"`
	Addr string `json:"addr"`
	// Version   string            `json:"version"`
	// Weight    int               `json:"weight"`
	Metadata map[string]string `json:"metadata"`
	// Endpoints []string          `json:"endpoints"`
}

func (s *ServiceInfo) Unmarshal(b []byte) error {
	buf := bytes.NewBuffer(b)
	decoder := json.NewDecoder(buf)
	decoder.UseNumber()
	decoder.Decode(s)
	return nil
}

func (s *ServiceInfo) Marshal() string {
	b, _ := json.Marshal(s)
	return string(b)
}
