// Code generated by beauty; DO NOT EDIT.

package router

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

type Route struct{
	Method string
	URI string
	Handler http.HandlerFunc
}

func NewRoutes() http.Handler{
	r := chi.NewRouter()
	for _, route := range routes {
		r.Method(route.Method, route.URI, route.Handler)
	}
	return r
}