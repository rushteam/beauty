package beauty

import (
	"net/http"

	"github.com/go-chi/chi/middleware"
)

var WebLogger = middleware.Logger
var WebRecoverer = middleware.Recoverer

var DefaultMiddlewares = []func(next http.Handler) http.Handler{
	middleware.Logger,
	middleware.Recoverer,
}
