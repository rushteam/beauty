package ast

//Stmt ..
type Stmt struct {
	Services []Service
}

//Service ..
type Service struct {
	Name string
	Rpcs []RPC
}

//RPC ..
type RPC struct {
	Routes   []Route
	Handler  string
	Request  string
	Response string
}

//Route ..
type Route struct {
	Methods []string
	URI     string
}

//At ..
type At struct {
	Opt string
	Val map[string]string
}
