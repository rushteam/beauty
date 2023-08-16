package beauty

import "net/http"

type Route struct {
	Method  string
	URI     string
	Handler http.HandlerFunc
}
