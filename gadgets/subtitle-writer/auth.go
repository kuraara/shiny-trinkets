package main

// Bearer-token auth (as in Ask Me, without the browser cookie: there is no page). Every request
// needs "Authorization: Bearer <TOKEN>", except /healthz.

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

func withAuth(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		b, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if found && subtle.ConstantTimeCompare([]byte(strings.TrimSpace(b)), []byte(token)) == 1 {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="subtitle-writer"`)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "token needed: send Authorization: Bearer <token>"})
	})
}
