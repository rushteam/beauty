package parser

import (
	"bufio"
	"fmt"
	"io"
)

//GoWhitespace ..
const GoWhitespace int = 1<<'\t' | 1<<'\n' | 1<<'\r' | 1<<' '

// const (
// 	EOF rune = iota
// 	Ident
// 	Comment

// 	// internal use only
// 	skipComment
// )
var tokenString = map[rune]string{
	EOF: "EOF",
	// Ident:   "Ident",
	// Comment: "Comment",
}

//TokenString ..
func TokenString(tok rune) string {
	// scanner.Comment
	if s, found := tokenString[tok]; found {
		return s
	}
	return fmt.Sprintf("%q", string(tok))
}

// Scanner represents a lexical scanner.
type Scanner struct {
	r          *bufio.Reader
	Whitespace int
	line       int // line count
	column     int // character count
}

// NewScanner returns a new instance of Scanner.
func NewScanner(src io.Reader) *Scanner {
	return &Scanner{
		r:          bufio.NewReader(src),
		line:       1,
		column:     0,
		Whitespace: GoWhitespace,
	}
}

//Scan ..
func (s *Scanner) Scan() rune {
	ch := s.Peek()
	// redo:
	// skip white space
	for s.Whitespace&(1<<uint(ch)) != 0 {
		ch = s.Peek()
	}

	// start collecting token text

	// determine token value
	tok := ch
	switch ch {
	case '@':
		ch = s.scanAt(ch)
	// case isDecimal(ch):
	// 	tok, ch = s.scanNumber(ch, false)
	default:
		switch ch {
		case EOF:
			break
		case '/':
			ch = s.Peek()
			if ch == '/' || ch == '*' {
				ch = s.scanComment(ch)
				tok = Comment
			}
		default:
			ch = s.Peek()
		}
	}
	return tok
}

// Peek ..
func (s *Scanner) Peek() rune {
	ch, _, err := s.r.ReadRune()
	if err != nil {
		return EOF
	}
	switch ch {
	case '\n':
		s.line++
		s.column = 0
	}
	return ch
}
func (s *Scanner) unread() {
	_ = s.r.UnreadRune()
}

func (s *Scanner) scanAt(ch rune) rune {
	return ch
}

func (s *Scanner) scanComment(ch rune) rune {
	// ch == '/' || ch == '*'
	if ch == '/' {
		// line comment
		ch = s.Peek() // read character after "//"
		for ch != '\n' && ch >= 0 {
			ch = s.Peek()
		}
		return ch
	}

	// general comment
	ch = s.Peek() // read character after "/*"
	for {
		if ch < 0 {
			// s.error("comment not terminated")
			break
		}
		ch0 := ch
		ch = s.Peek()
		if ch0 == '*' && ch == '/' {
			ch = s.Peek()
			break
		}
	}
	return ch
}
