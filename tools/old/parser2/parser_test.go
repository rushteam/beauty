package parser

import (
	"strings"
	"testing"
)

func TestNewParser(t *testing.T) {
	content := `
@doc(summary: desc in one line)
@auth jwt
service (
    @rest /index/:name
    rpc Index(getRequest) returns (getResponse)
)
`
	p := NewParser(strings.NewReader(content))
	t.Logf("%+v", p)
	p.Parse()
	t.Fail()
}
