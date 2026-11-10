package main

// Token auth. With TOKEN set, every request needs it, except /healthz and the icon:
//   - agents send "Authorization: Bearer <TOKEN>" (the MCP endpoint and the API)
//   - a browser opens the page once as /?key=<TOKEN>; that sets a cookie for a year and
//     redirects to the plain address, so bookmarks don't carry the key.
// Without TOKEN, everything is open (and the log says so).

import (
	"crypto/subtle"
	"net/http"
	"strings"
	"time"
)

const cookieName = "planrunner_key"

func withAuth(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	ok := func(s string) bool { return subtle.ConstantTimeCompare([]byte(s), []byte(token)) == 1 }
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" || r.URL.Path == "/icon.svg" {
			next.ServeHTTP(w, r)
			return
		}
		if k := r.URL.Query().Get("key"); k != "" {
			if !ok(k) {
				locked(w, r, "That sign-in link has the wrong key.")
				return
			}
			http.SetCookie(w, &http.Cookie{Name: cookieName, Value: token, Path: "/", HttpOnly: true,
				SameSite: http.SameSiteLaxMode, MaxAge: int((400 * 24 * time.Hour).Seconds())})
			q := r.URL.Query()
			q.Del("key")
			u := *r.URL
			u.RawQuery = q.Encode()
			http.Redirect(w, r, u.RequestURI(), http.StatusSeeOther)
			return
		}
		if b, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); found && ok(strings.TrimSpace(b)) {
			next.ServeHTTP(w, r)
			return
		}
		if c, err := r.Cookie(cookieName); err == nil && ok(c.Value) {
			next.ServeHTTP(w, r)
			return
		}
		locked(w, r, "")
	})
}

func locked(w http.ResponseWriter, r *http.Request, why string) {
	if strings.HasPrefix(r.URL.Path, "/mcp") || strings.HasPrefix(r.URL.Path, "/api/") {
		w.Header().Set("WWW-Authenticate", `Bearer realm="plan-runner"`)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "token needed: send Authorization: Bearer <token>"})
		return
	}
	if why == "" {
		why = "This page needs its key. Open the sign-in link you were given once on this device; after that the plain address works."
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusUnauthorized)
	w.Write([]byte(`<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Plan Runner</title>` +
		`<body style="font:16px/1.5 system-ui,sans-serif;max-width:32rem;margin:15vh auto;padding:0 16px"><h1>Plan Runner is locked</h1><p>` + why + `</p></body>`))
}
