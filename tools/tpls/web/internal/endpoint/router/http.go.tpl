// Code generated by beauty; DO NOT EDIT.

package router

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type Route struct {
	Method string
	URI string
	Handler http.HandlerFunc
}

func NewRouter(routes []Route) http.Handler{
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.CleanPath)
	for _, route := range routes {
		r.Method(route.Method, route.URI, route.Handler)
	}
	return r
}