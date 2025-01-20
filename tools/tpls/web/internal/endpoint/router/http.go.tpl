// Code generated by beauty; DO NOT EDIT.

package router

import (
	"net/http"
	"{{.ImportPath}}internal/config"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/rushteam/beauty"
)

type Route struct{
	Method string
	URI string
	Handler http.HandlerFunc
}

func New(conf *config.Config) beauty.Option{
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	for _, route := range routes() {
		r.Method(route.Method, route.URI, route.Handler)
	}

	return beauty.WithWebServer(
		conf.HTTP.Addr,
		r,
		beauty.WithServiceName(conf.App),
	)
}