// Package routerinit registers routes from declared init functions and from
// a package-level initializer (review S2). registered_in names pkg.init#1,
// pkg.init#2 and a closure of the synthesized pkg.init; each must be an
// emitted symbol of this unit so the shard is referentially complete.
package routerinit

import "net/http"

// Auth is the approved middleware.
func Auth(next http.Handler) http.Handler { return next }

func ok(w http.ResponseWriter, r *http.Request) {}

func init() {
	http.Handle("/first", Auth(http.HandlerFunc(ok)))
}

func init() {
	http.HandleFunc("/second", ok)
}

// Admin is built by a closure in the package initializer.
var Admin = func() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /admin", Auth(http.HandlerFunc(ok)))
	return mux
}()
