package parser

import (
	"bufio"
)

type baseState struct {
	r          *bufio.Reader
	lineNumber *int
}

//RootState ..
type RootState struct{}

//NewParser ..
func NewParser(src string) *SvcAst {
	if err != nil {
		return nil, err
	}
	sa := &SvcAst{}
	return sa
}
