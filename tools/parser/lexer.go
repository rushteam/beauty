package parser

import (
	"fmt"
	"io"
)

//NewScanner ..
func NewScanner(src io.Reader) BeautyLexer {
	s := &Scanner{}
	s.Init(src)
	s.ErrorFunc = func(s *Scanner, msg string) {
		fmt.Printf("scanner: %s %s\n", msg, s.Pos())
	}
	return s
}

//Lex ..
func (s *Scanner) Error(msg string) {
	if s.ErrorFunc != nil {
		s.ErrorFunc(s, msg)
	}
}

//Lex ..
func (s *Scanner) Lex(lval *BeautySymType) int {
	tok, val := s.Scan()
	//ILLEGAL
	fmt.Println("token:", tok, val)
	lval.token = val
	return int(tok)
}
