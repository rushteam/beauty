package parser

const (
	//TYPE  ..
	TYPE int = iota
	//SERVICE  ..
	SERVICE
	//AT ..
	AT //@
	//REST ..
	REST //rest
	//RPC ..
	RPC //rpc
	//RET ..
	RET //returns
)

// type getRequest struct {
//     Name string `json:"city"`
// }

// @doc(summary: desc in one line)
// @auth jwt
// service (
//     @route /index/:name
//     rest Index(getRequest) returns (getResponse)
//     rpc Index(getRequest) returns (getResponse)
// )
