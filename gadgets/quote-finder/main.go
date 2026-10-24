// Quote Finder: search the dialogue of everything in the Plex library.
// Subtitles arrive from the extractor (running next to the media) through /api/ingest.
package main

import (
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

//go:embed web
var webFiles embed.FS

type app struct {
	db    *store
	token string
}

func main() {
	dataDir := env("DATA_DIR", "/data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		log.Fatal(err)
	}
	db, err := openDB(filepath.Join(dataDir, "quotes.db"))
	if err != nil {
		log.Fatal(err)
	}
	a := &app{db: db, token: os.Getenv("INGEST_TOKEN")}
	if a.token == "" {
		log.Print("INGEST_TOKEN is not set: the extractor can't send subtitles")
	}
	srv := &http.Server{Addr: env("ADDR", ":8080"), Handler: a.routes(), ReadHeaderTimeout: 10 * time.Second}
	log.Printf("quote-finder listening on %s", srv.Addr)
	log.Fatal(srv.ListenAndServe())
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func (a *app) routes() http.Handler {
	web, _ := fs.Sub(webFiles, "web")
	mux := http.NewServeMux()
	mux.Handle("GET /", noCache(http.FileServerFS(web)))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ok\n") })
	mux.HandleFunc("GET /api/search", a.search)
	mux.HandleFunc("GET /api/context", a.context)
	mux.HandleFunc("GET /api/titles", a.titles)
	mux.HandleFunc("GET /api/stats", a.stats)
	mux.HandleFunc("GET /api/ingest/known", a.auth(a.known))
	mux.HandleFunc("POST /api/ingest", a.auth(a.ingest))
	mux.HandleFunc("POST /api/ingest/forget", a.auth(a.forget))
	return mux
}

func noCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		h.ServeHTTP(w, r)
	})
}

func (a *app) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if a.token == "" || subtle.ConstantTimeCompare([]byte(got), []byte(a.token)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "bad token"})
			return
		}
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, err error) {
	log.Print(err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
}

func (a *app) search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	f := filter{Kind: q.Get("kind"), Title: q.Get("title"), Regex: q.Get("regex") == "1"}
	if f.Kind != "tv" && f.Kind != "movie" {
		f.Kind = ""
	}
	res, err := a.db.search(q.Get("q"), f, limit)
	var bq badQuery
	if errors.As(err, &bq) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": bq.msg})
		return
	}
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (a *app) context(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	id, _ := strconv.ParseInt(q.Get("media"), 10, 64)
	at, _ := strconv.Atoi(q.Get("at"))
	lines, err := a.db.context(id, at, 12, 12)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, lines)
}

func (a *app) titles(w http.ResponseWriter, r *http.Request) {
	t, err := a.db.titles()
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (a *app) stats(w http.ResponseWriter, r *http.Request) {
	st, err := a.db.stats()
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (a *app) known(w http.ResponseWriter, r *http.Request) {
	k, err := a.db.known()
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, k)
}

func (a *app) ingest(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 32<<20)
	var it ingestItem
	if err := json.NewDecoder(r.Body).Decode(&it); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad body"})
		return
	}
	switch it.Status {
	case "indexed", "none", "image", "error":
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad status"})
		return
	}
	if it.Path == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": errors.New("no path").Error()})
		return
	}
	n, err := a.db.put(it)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"lines": n})
}

func (a *app) forget(w http.ResponseWriter, r *http.Request) {
	var paths []string
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&paths); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad body"})
		return
	}
	n, _ := a.db.forget(paths)
	writeJSON(w, http.StatusOK, map[string]int{"removed": n})
}
