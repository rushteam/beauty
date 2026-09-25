package expr

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// tokenKind 词法单元类型。
type tokenKind int

const (
	tEOF tokenKind = iota
	tNumber
	tString
	tIdent
	tTrue
	tFalse
	tNil
	tPlus
	tMinus
	tStar
	tSlash
	tPercent
	tEq  // ==
	tNeq // !=
	tLt
	tLte
	tGt
	tGte
	tAnd // &&
	tOr  // ||
	tNot // !
	tLParen
	tRParen
	tLBracket
	tRBracket
	tComma
	tDot
	tIn
)

// token 一个词法单元。
type token struct {
	kind tokenKind
	str  string  // 标识符/字符串原文
	num  float64 // 数字值
	pos  int     // 起始位置(用于报错)
}

// lex 把输入切成 token 序列(以 tEOF 结尾)。
func lex(input string) ([]token, error) {
	var toks []token
	runes := []rune(input)
	i := 0
	n := len(runes)
	for i < n {
		c := runes[i]
		switch {
		case unicode.IsSpace(c):
			i++
		case c >= '0' && c <= '9' || (c == '.' && i+1 < n && runes[i+1] >= '0' && runes[i+1] <= '9'):
			start := i
			for i < n && (runes[i] >= '0' && runes[i] <= '9' || runes[i] == '.') {
				i++
			}
			numStr := string(runes[start:i])
			f, err := strconv.ParseFloat(numStr, 64)
			if err != nil {
				return nil, fmt.Errorf("expr: invalid number %q at %d", numStr, start)
			}
			toks = append(toks, token{kind: tNumber, num: f, pos: start})
		case c == '"' || c == '\'':
			quote := c
			i++
			start := i
			var sb strings.Builder
			for i < n && runes[i] != quote {
				if runes[i] == '\\' && i+1 < n { // 支持简单转义
					i++
					switch runes[i] {
					case 'n':
						sb.WriteRune('\n')
					case 't':
						sb.WriteRune('\t')
					case '\\':
						sb.WriteRune('\\')
					case quote:
						sb.WriteRune(quote)
					default:
						sb.WriteRune(runes[i])
					}
				} else {
					sb.WriteRune(runes[i])
				}
				i++
			}
			if i >= n {
				return nil, fmt.Errorf("expr: unterminated string at %d", start-1)
			}
			i++ // 跳过收尾引号
			toks = append(toks, token{kind: tString, str: sb.String(), pos: start})
		case isIdentStart(c):
			start := i
			for i < n && isIdentPart(runes[i]) {
				i++
			}
			word := string(runes[start:i])
			switch word {
			case "true":
				toks = append(toks, token{kind: tTrue, pos: start})
			case "false":
				toks = append(toks, token{kind: tFalse, pos: start})
			case "nil", "null":
				toks = append(toks, token{kind: tNil, pos: start})
			case "in":
				toks = append(toks, token{kind: tIn, pos: start})
			case "and":
				toks = append(toks, token{kind: tAnd, pos: start})
			case "or":
				toks = append(toks, token{kind: tOr, pos: start})
			case "not":
				toks = append(toks, token{kind: tNot, pos: start})
			default:
				toks = append(toks, token{kind: tIdent, str: word, pos: start})
			}
		default:
			// 运算符与标点
			tk, adv, err := lexOperator(runes, i)
			if err != nil {
				return nil, err
			}
			toks = append(toks, tk)
			i += adv
		}
	}
	toks = append(toks, token{kind: tEOF, pos: i})
	return toks, nil
}

func lexOperator(runes []rune, i int) (token, int, error) {
	c := runes[i]
	n := len(runes)
	two := func(next rune, k tokenKind) (token, int, bool) {
		if i+1 < n && runes[i+1] == next {
			return token{kind: k, pos: i}, 2, true
		}
		return token{}, 0, false
	}
	switch c {
	case '+':
		return token{kind: tPlus, pos: i}, 1, nil
	case '-':
		return token{kind: tMinus, pos: i}, 1, nil
	case '*':
		return token{kind: tStar, pos: i}, 1, nil
	case '/':
		return token{kind: tSlash, pos: i}, 1, nil
	case '%':
		return token{kind: tPercent, pos: i}, 1, nil
	case '(':
		return token{kind: tLParen, pos: i}, 1, nil
	case ')':
		return token{kind: tRParen, pos: i}, 1, nil
	case '[':
		return token{kind: tLBracket, pos: i}, 1, nil
	case ']':
		return token{kind: tRBracket, pos: i}, 1, nil
	case ',':
		return token{kind: tComma, pos: i}, 1, nil
	case '.':
		return token{kind: tDot, pos: i}, 1, nil
	case '=':
		if tk, adv, ok := two('=', tEq); ok {
			return tk, adv, nil
		}
		return token{}, 0, fmt.Errorf("expr: unexpected '=' at %d (did you mean '=='?)", i)
	case '!':
		if tk, adv, ok := two('=', tNeq); ok {
			return tk, adv, nil
		}
		return token{kind: tNot, pos: i}, 1, nil
	case '<':
		if tk, adv, ok := two('=', tLte); ok {
			return tk, adv, nil
		}
		return token{kind: tLt, pos: i}, 1, nil
	case '>':
		if tk, adv, ok := two('=', tGte); ok {
			return tk, adv, nil
		}
		return token{kind: tGt, pos: i}, 1, nil
	case '&':
		if tk, adv, ok := two('&', tAnd); ok {
			return tk, adv, nil
		}
		return token{}, 0, fmt.Errorf("expr: unexpected '&' at %d (did you mean '&&'?)", i)
	case '|':
		if tk, adv, ok := two('|', tOr); ok {
			return tk, adv, nil
		}
		return token{}, 0, fmt.Errorf("expr: unexpected '|' at %d (did you mean '||'?)", i)
	}
	return token{}, 0, fmt.Errorf("expr: unexpected character %q at %d", string(c), i)
}

func isIdentStart(c rune) bool {
	return c == '_' || unicode.IsLetter(c)
}

func isIdentPart(c rune) bool {
	return c == '_' || unicode.IsLetter(c) || unicode.IsDigit(c)
}
