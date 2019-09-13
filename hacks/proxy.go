package hacks

import (
	"net/http"
	"net/http/httputil"
)

func WorkingKey(w http.ResponseWriter, r *http.Request) {

	rev := httputil.ReverseProxy{}
	rev.ServeHTTP(w, r)
}
