package parser

//Token ..
type Token int

const (
	//ILLEGAL ..
	ILLEGAL Token = iota
	//EOF ..
	EOF
	//IDENT ..
	IDENT

	keywordsStart
	//TokenType ..
	TokenType
	//TokenService  ..
	TokenService
	//TokenAt ..
	TokenAt
	//TokenRest ..
	TokenRest
	//TokenRpc ..
	TokenRpc
	//TokenReturn returns
	TokenReturn
	keywordsEnd
)

var keywords = map[Token]string{
	TokenType:    "type",
	TokenService: "service",
	TokenAt:      "@",
	TokenRest:    "rest",
	TokenRpc:     "rpc",
	TokenReturn:  "returns",
}

var eof rune = 0

func isWhitespace(ch rune) bool {
	return ch == ' ' || ch == '\t'
}
func isLine(ch rune) bool {
	return ch == '\n' || ch == '\r'
}

func isLetter(ch rune) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
}

// type getRequest struct {
//     Name string `json:"city"`
// }

// @doc(summary: desc in one line)
// @auth jwt
// service (
//     @rest /index/:name
//     rpc Index(getRequest) returns (getResponse)
// )
