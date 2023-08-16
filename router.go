package beauty

import "net/http"

type Route struct {
	URI     string
	Method  string
	Handler http.HandlerFunc
}
