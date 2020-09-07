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
%type <val> at comment

// same for terminals
// SERVICE RPC
%token ILLEGAL EOF At
%token <val> at_auth at_val
%token <val> string
%%
at: at_auth at_val {
	fmt.Println($1)
	$$ = $2
}
comment: string{
	$$ = $1
}

%%
/*  start  of  programs  */