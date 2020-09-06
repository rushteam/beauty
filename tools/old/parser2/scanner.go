package parser

import (
	"bufio"
	"bytes"
	"io"
	"strings"
)

// Scanner represents a lexical scanner.
type Scanner struct {
	r *bufio.Reader
}

// NewScanner returns a new instance of Scanner.
func NewScanner(r io.Reader) *Scanner {
	return &Scanner{r: bufio.NewReader(r)}
}

// read reads the next rune from the bufferred reader.
// Returns the rune(0) if an error occurs (or io.EOF is returned).
func (s *Scanner) read() rune {
	ch, _, err := s.r.ReadRune()
	if err != nil {
		return eof
	}
	return ch
}

// unread places the previously read rune back on the reader.
func (s *Scanner) unread() { _ = s.r.UnreadRune() }

// Scan returns the next token and literal value.
func (s *Scanner) Scan() (Token, string) {
	// Read the next rune.
	var buf bytes.Buffer
	for {
		ch := s.read()
		if isWhitespace(ch) {
			continue
		} else if isLine(ch) {
			continue
		} else if ch == 0 {
			return EOF, ""
		}
		// s.unread()
		buf.WriteRune(ch)
		switch strings.ToLower(buf.String()) {
		case keywords[TokenAt]:
			return TokenAt, buf.String()
		case keywords[TokenService]:
			return TokenService, buf.String()
		default:
			return ILLEGAL, ""
		}
	}
}
