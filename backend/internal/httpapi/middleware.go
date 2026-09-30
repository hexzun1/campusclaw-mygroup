package httpapi

import "net/http"

// NoStore marks every response it wraps as non-cacheable. Bearer-authenticated
// responses MUST NOT be served from a cache: caches key on the URL and do not
// vary on the Authorization header, so a cached 200 could be replayed after the
// caller has logged out (design.md Decision 7). The header is set before the
// inner handler runs, so it is present on success and on every error response
// (401, 404, 503) alike.
func NoStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}
