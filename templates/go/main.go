// __NAME__: a small Go web app (standard library only). The page and its files are embedded;
// /api/... answers JSON; /healthz is for Docker and the registry's status dot.
package main

import (
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

//go:embed web
var webFiles embed.FS

func main() {
	addr := ":8080"
	if a := os.Getenv("ADDR"); a != "" {
		addr = a
	}
	srv := &http.Server{Addr: addr, Handler: routes(), ReadHeaderTimeout: 10 * time.Second}
	log.Printf("__ID__ listening on %s", addr)
	log.Fatal(srv.ListenAndServe())
}

func routes() http.Handler {
	web, _ := fs.Sub(webFiles, "web")
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServerFS(web))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok\n")) })
	mux.HandleFunc("GET /api/hello", hello)
	return mux
}

func hello(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if len(name) > 60 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "that name is a bit long"})
		return
	}
	if name == "" {
		name = "there"
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "Hello, " + name + "!"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
