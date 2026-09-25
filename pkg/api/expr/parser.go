package expr

import "fmt"

// parser 是一个基于优先级的递归下降(Pratt 风格)解析器。
//
// 优先级(从低到高):
//
//	|| → && → 比较(== != < <= > >= in) → 加减(+ -) → 乘除模(* / %) → 一元(- ! +) → 后缀(. 调用) → 原子
type parser struct {
	tokens []token
	pos    int
}

func (p *parser) peek() token { return p.tokens[p.pos] }

func (p *parser) next() token {
	t := p.tokens[p.pos]
	if p.pos < len(p.tokens)-1 {
		p.pos++
	}
	return t
}

func (p *parser) expect(k tokenKind, what string) error {
	if p.peek().kind != k {
		return fmt.Errorf("expr: expected %s at pos %d", what, p.peek().pos)
	}
	p.next()
	return nil
}

// parse 解析整个输入,要求消费到 EOF。
func (p *parser) parse() (node, error) {
	n, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.peek().kind != tEOF {
		return nil, fmt.Errorf("expr: unexpected trailing token at pos %d", p.peek().pos)
	}
	return n, nil
}

func (p *parser) parseOr() (node, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tOr {
		p.next()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = logicalNode{op: tOr, left: left, right: right}
	}
	return left, nil
}

func (p *parser) parseAnd() (node, error) {
	left, err := p.parseComparison()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tAnd {
		p.next()
		right, err := p.parseComparison()
		if err != nil {
			return nil, err
		}
		left = logicalNode{op: tAnd, left: left, right: right}
	}
	return left, nil
}

func (p *parser) parseComparison() (node, error) {
	left, err := p.parseAdditive()
	if err != nil {
		return nil, err
	}
	for {
		k := p.peek().kind
		if k == tEq || k == tNeq || k == tLt || k == tLte || k == tGt || k == tGte || k == tIn {
			p.next()
			right, err := p.parseAdditive()
			if err != nil {
				return nil, err
			}
			left = binaryNode{op: k, left: left, right: right}
		} else {
			return left, nil
		}
	}
}

func (p *parser) parseAdditive() (node, error) {
	left, err := p.parseMultiplicative()
	if err != nil {
		return nil, err
	}
	for {
		k := p.peek().kind
		if k == tPlus || k == tMinus {
			p.next()
			right, err := p.parseMultiplicative()
			if err != nil {
				return nil, err
			}
			left = binaryNode{op: k, left: left, right: right}
		} else {
			return left, nil
		}
	}
}

func (p *parser) parseMultiplicative() (node, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		k := p.peek().kind
		if k == tStar || k == tSlash || k == tPercent {
			p.next()
			right, err := p.parseUnary()
			if err != nil {
				return nil, err
			}
			left = binaryNode{op: k, left: left, right: right}
		} else {
			return left, nil
		}
	}
}

func (p *parser) parseUnary() (node, error) {
	k := p.peek().kind
	if k == tMinus || k == tNot || k == tPlus {
		p.next()
		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return unaryNode{op: k, operand: operand}, nil
	}
	return p.parsePostfix()
}

// parsePostfix 解析原子后缀:成员访问 .field 与函数调用 ident(...)。
func (p *parser) parsePostfix() (node, error) {
	n, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tDot {
		p.next()
		if p.peek().kind != tIdent {
			return nil, fmt.Errorf("expr: expected field name after '.' at pos %d", p.peek().pos)
		}
		field := p.next().str
		n = memberNode{obj: n, field: field}
	}
	return n, nil
}

func (p *parser) parsePrimary() (node, error) {
	t := p.peek()
	switch t.kind {
	case tNumber:
		p.next()
		return literalNode{val: t.num}, nil
	case tString:
		p.next()
		return literalNode{val: t.str}, nil
	case tTrue:
		p.next()
		return literalNode{val: true}, nil
	case tFalse:
		p.next()
		return literalNode{val: false}, nil
	case tNil:
		p.next()
		return literalNode{val: nil}, nil
	case tIdent:
		p.next()
		// 函数调用?
		if p.peek().kind == tLParen {
			return p.parseCall(t.str)
		}
		return identNode{name: t.str}, nil
	case tLParen:
		p.next()
		n, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if err := p.expect(tRParen, "')'"); err != nil {
			return nil, err
		}
		return n, nil
	case tLBracket:
		return p.parseArray()
	default:
		return nil, fmt.Errorf("expr: unexpected token at pos %d", t.pos)
	}
}

func (p *parser) parseCall(name string) (node, error) {
	p.next() // 消费 '('
	var args []node
	if p.peek().kind != tRParen {
		for {
			arg, err := p.parseOr()
			if err != nil {
				return nil, err
			}
			args = append(args, arg)
			if p.peek().kind == tComma {
				p.next()
				continue
			}
			break
		}
	}
	if err := p.expect(tRParen, "')'"); err != nil {
		return nil, err
	}
	return callNode{name: name, args: args}, nil
}

func (p *parser) parseArray() (node, error) {
	p.next() // 消费 '['
	var elems []node
	if p.peek().kind != tRBracket {
		for {
			e, err := p.parseOr()
			if err != nil {
				return nil, err
			}
			elems = append(elems, e)
			if p.peek().kind == tComma {
				p.next()
				continue
			}
			break
		}
	}
	if err := p.expect(tRBracket, "']'"); err != nil {
		return nil, err
	}
	return arrayNode{elems: elems}, nil
}
