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
%token ILLEGAL EOF Comment
%token <val> at_key val
%%
at: at_key {
	fmt.Println($1)
	$$ = $1
} | at_key val {
	fmt.Println($1,$2)
	$$ = $1
}

%%
/*  start  of  programs  */