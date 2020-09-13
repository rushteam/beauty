package ast

//Node ..
type Node interface {
	// Walk(visit Visit) error
	// walkSub()
}

// ast.Walk(v, f)

//Stmt ..
type Stmt struct {
	Node
	Services []*Service
}

//Service ..
type Service struct {
	Node
	Name string
	Rpcs []*RPC
}

//RPC ..
type RPC struct {
	Node
	Routes   []*Route
	Handler  string
	Request  string
	Response string
}

//Route ..
type Route struct {
	Node
	Methods []string
	URI     string
}

//At ..
// type At struct {
// 	Node
// 	Opt string
// 	Val map[string]string
// }
