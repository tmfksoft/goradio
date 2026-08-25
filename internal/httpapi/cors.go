package httpapi

import "net/http"

// corsMiddleware allows any origin to read from this listener-facing API.
// Every route behind it is already public and unauthenticated by design (see
// the package doc), or, for now-playing's optional bearer-token path, gated
// by a token the caller supplies explicitly rather than ambient browser
// credentials -- so a wildcard origin exposes nothing that isn't already
// public. This is what lets browser JS on an arbitrary third-party site (an
// embedded now-playing widget, say) call /stations/{slug}/now-playing at
// all: without it the response arrives fine but the browser discards it
// before handing it to fetch().
//
// OPTIONS is intercepted here, before mux dispatch, so a preflight succeeds
// even for a slug the mux would otherwise 404 on -- a plain *http.ServeMux
// only registers these routes for GET, so an unhandled OPTIONS would
// otherwise fail before ever reaching that logic. Authorization is the only
// header this API accepts, and it's the only one that turns a cross-origin
// GET into a preflighted "non-simple" request, so it's the only one allowed.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")

		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization")
			w.Header().Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
