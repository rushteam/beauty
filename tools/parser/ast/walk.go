package ast

import "fmt"

//Visitor ..
type Visitor interface {
	Visit(node Node) (w Visitor)
}

//Walk ..
func Walk(v Visitor, node Node) {
	if v = v.Visit(node); v == nil {
		return
	}
	// walk children
	// (the order of the cases matches the order
	// of the corresponding node types in ast.go)
	switch n := node.(type) {
	case []*Stmt:
		for _, stmt := range n {
			Walk(v, stmt)
		}
	case *Stmt:
		for _, c := range n.Services {
			Walk(v, c)
		}
	case *Service:
		for _, c := range n.Rpcs {
			Walk(v, c)
		}
	case *RPC:
		for _, c := range n.Routes {
			Walk(v, c)
		}
	case *Route:
		// nothing to do

	default:
		panic(fmt.Sprintf("ast.Walk: unexpected node type %T", n))
	}
	v.Visit(nil)
}

type inspector func(Node) bool

func (f inspector) Visit(node Node) Visitor {
	if f(node) {
		return f
	}
	return nil
}

// Inspect traverses an AST in depth-first order: It starts by calling
// f(node); node must not be nil. If f returns true, Inspect invokes f
// recursively for each of the non-nil children of node, followed by a
// call of f(nil).
//
func Inspect(node Node, f func(Node) bool) {
	Walk(inspector(f), node)
}
