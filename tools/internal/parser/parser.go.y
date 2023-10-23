%{
package parser

import (
	"github.com/rushteam/beauty/tools/internal/parser/ast"
)

const EOF =0
// go install golang.org/x/tools/cmd/goyacc@latest
// goyacc -o parser.go parser.go.y
%}
// fields inside this union end up as the fields in a structure known
// as ${PREFIX}SymType, of which a reference is passed to the lexer.
%union{
	// empty struct{}
	token string
	str string
	services []*ast.Service
	service *ast.Service
	rpcs []*ast.RPC
	rpc *ast.RPC
	route *ast.Route
	routes []*ast.Route
	route_methods []string
}

// any non-terminal which returns a value needs a type, which is
// really a field name in the above union struct
// %type <val> expr number

%type <token> program
%type <services> services
%type <service> service
%type <rpc> rpc
%type <rpcs> rpcs
%type <route> route
%type <routes> routes
%type <route_methods> route_methods


%start program

// SERVICE RPC
%token ILLEGAL
%token Service Rpc Returns Route
%token <token> Comment Ident String
%token '{' '}' '(' ')' '|' '.'

%%
program: {}
| services {
	if l, ok := yylex.(*Scanner); ok {
		stmt := &ast.Stmt{
			Services: $1,
		}
		l.Stmts = append(l.Stmts,stmt)
	}
};

services: services service {
	$$ = append($1,$2)
} | service {
	$$ = append($$,$1)
}

service: Service Ident '{' rpcs '}' {
	$$ = &ast.Service{
		Name: $2,
		Rpcs: $4,
	}
} | Service Ident '{' '}' {
	$$ = &ast.Service{
		Name: $2,
	}
};

rpcs: rpcs rpc {
	$$ = append($1,$2)
} | rpc {
	$$ = append($$,$1)
};

rpc: routes Rpc Ident '.' Ident '(' Ident ')' Returns '(' Ident ')' {
	$$ = &ast.RPC{
		Routes: $1,
		Handler: $3 + "." + $5,
		Request: $7,
		Response: $11,
	}
} | routes Rpc Ident '(' Ident ')' Returns '(' Ident ')' {
	$$ = &ast.RPC{
		Routes: $1,
		Handler: $3,
		Request: $5,
		Response: $9,
	}
} | Rpc Ident '.' Ident '(' Ident ')' Returns '(' Ident ')' {
	$$ = &ast.RPC{
		Routes: []*ast.Route{},
		Handler: $2 + "." + $4,
		Request: $6,
		Response: $10,
	}
} | Rpc Ident '(' Ident ')' Returns '(' Ident ')' {
	$$ = &ast.RPC{
		Routes: []*ast.Route{},
		Handler: $2,
		Request: $4,
		Response: $8,
	}
};

routes: routes route {
	$$ = append($1,$2)
} | route {
	$$ = append($$,$1)
};

route: Route route_methods String {
	$$ = &ast.Route{
		Methods: $2,
		URI: $3,
	}
};

route_methods: route_methods '|' Ident {
	$$ = append($1,$3)
} | Ident {
	$$ = append($$,$1)
};
%%
/*  start  of  programs  */

/*
service helloworld {
	@route GET|POST "/index/:id"
    rpc Index(getRequest) returns (getResponse)
    rpc Helloworld(getRequest) returns (getResponse)
}
*/