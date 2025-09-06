package ast

// Node ..
type Node interface {
	// Walk(visit Visit) error
	// walkSub()
}

// ast.Walk(v, f)

// Stmt ..
type Stmt struct {
	Node
	Services []*Service
}

// Service ..
type Service struct {
	Node
	Name string
	Rpcs []*RPC
}

// RPC ..
type RPC struct {
	Node
	Routes   []*Route
	Handler  string
	Request  string
	Response string
}

// Route ..
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

// ProtobufFile 表示一个protobuf文件
type ProtobufFile struct {
	Node
	Filename  string
	Syntax    string
	Package   string
	GoPackage string
	Imports   []string
	Services  []*ProtobufService
	Messages  []*ProtobufMessage
}

// ProtobufService 表示protobuf服务
type ProtobufService struct {
	Node
	Name string
	RPCs []*ProtobufRPC
}

// ProtobufRPC 表示protobuf RPC方法
type ProtobufRPC struct {
	Node
	Name        string
	Request     string
	Response    string
	LineNum     int
	HTTPOptions *HTTPOptions
}

// ProtobufMessage 表示protobuf消息
type ProtobufMessage struct {
	Node
	Name   string
	Fields []*ProtobufField
}

// ProtobufField 表示protobuf字段
type ProtobufField struct {
	Node
	Type string
	Name string
	Tag  string
}

// HTTPOptions 表示HTTP选项
type HTTPOptions struct {
	Node
	Method       string
	Path         string
	Body         string
	ResponseBody string
	Additional   []*HTTPOptions
}
