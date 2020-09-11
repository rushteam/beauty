%{

package parser

import (
	"./ast"
)

const EOF =0

//@auth jwt
//@rest url
//@doc (key:val)
%}
// fields inside this union end up as the fields in a structure known
// as ${PREFIX}SymType, of which a reference is passed to the lexer.
%union{
	// empty struct{}
	val string
	at ast.At
	service ast.Service
	rpc_slice []ast.RPC
	rpc ast.RPC
}

// any non-terminal which returns a value needs a type, which is
// really a field name in the above union struct
// %type <val> expr number
%type <val> program comment
%type <at> at
%type <service> service
%type <rpc> rpc
%type <rpc_slice> rpc_list

%start program
// same for terminals
// SERVICE RPC
%token ILLEGAL
%token Service Rpc Returns
%token <val> Comment Val Method
%token '(' ')'
%left '@' '|'

// service Helloworld (
//     @route "GET|POST" "/index/:id"
//     rpc Index(getRequest) returns (getResponse)
//     rpc Helloworld(getRequest) returns (getResponse)
// )
%%
program: {}
| program comment 
| program at {}
| program service {}
;
at: '@' Val Val {
	val := make(map[string]string,0)
	val["_"] = $3
	$$ = AtTok{
		Opt: $2,
		Val: val,
	}
};
comment: Comment {
	$$ = $1
};
service: Service Val '(' rpc_list ')' {
	$$ = ast.Service{

	}
};
rpc_list: rpc {
	$$ = []Rpc
	$$ = append($$,$1)
};
rpc: Rpc Val '(' Val ')' Returns '(' Val ')'{
	$$ = ast.RPC{}
};


%%
/*  start  of  programs  */