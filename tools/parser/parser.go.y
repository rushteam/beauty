%{

package parser

import (
	"fmt"
)
%}

// fields inside this union end up as the fields in a structure known
// as ${PREFIX}SymType, of which a reference is passed to the lexer.
%union{
	// empty struct{}
	val string
}

// any non-terminal which returns a value needs a type, which is
// really a field name in the above union struct
// %type <val> expr number
%type <val> at

// same for terminals
// SERVICE RPC
%token ILLEGAL EOF At Comment
%token <val> at_auth val
%%
at: at_auth val {
	fmt.Println($1)
	$$ = $2
}
%%
/*  start  of  programs  */