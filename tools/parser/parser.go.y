%{

package parser

import (
	"fmt"
)
//@auth jwt
//@rest url
//@doc (key:val)
const EOF =0
type AtTok struct {
	Opt string
	Val map[string]string
}
%}

// fields inside this union end up as the fields in a structure known
// as ${PREFIX}SymType, of which a reference is passed to the lexer.
%union{
	// empty struct{}
	val string
	at_tok AtTok
}

// any non-terminal which returns a value needs a type, which is
// really a field name in the above union struct
// %type <val> expr number
%type <val> program comment
%type <at_tok> at

%start program
// same for terminals
// SERVICE RPC
%token ILLEGAL
%token <val> Comment Val '@'

%%
program: comment 
| at
{
	fmt.Println($1)
}
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


%%
/*  start  of  programs  */