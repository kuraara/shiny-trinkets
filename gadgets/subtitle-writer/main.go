// Subtitle Writer: puts subtitle files next to videos on the media disk, for machines that
// make subtitles elsewhere (Whisper on the PC, translations). MCP at /mcp and a small JSON API,
// both behind a bearer token. It only writes `.srt` files it names itself, only next to videos
// that exist inside the media root, and only ever replaces or removes files it wrote (see
// writer.go). Standard library only; tiny, because it runs on the busy Pi.
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	w, err := setup(envOr("MEDIA_ROOT", "/media"), envOr("DATA", "/data")+"/written.json")
	if err != nil {
		log.Fatal(err)
	}
	if v := os.Getenv("NAME_STYLE"); v == "dot" || v == "dash" {
		nameStyle = v
	}
	token := os.Getenv("TOKEN")
	if token == "" {
		log.Fatal("TOKEN is required: this service writes to the media disk")
	}
	addr := envOr("ADDR", ":8080")
	srv := &http.Server{Addr: addr, Handler: withAuth(token, routes(w)), ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout: 2 * time.Minute, IdleTimeout: 2 * time.Minute}
	log.Printf("subtitle-writer on %s, media root %s, %d file(s) in the manifest", addr, w.root, len(w.man.entries))
	log.Fatal(srv.ListenAndServe())
}

func setup(root, manifestPath string) (*writer, error) {
	man, err := openManifest(manifestPath)
	if err != nil {
		return nil, err
	}
	return newWriter(root, man)
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

type putReq struct {
	Video string `json:"video"`
	SRT   string `json:"srt"`
	Label string `json:"label"`
	Lang  string `json:"lang"`
}

func routes(wr *writer) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok\n")) })
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("Subtitle Writer: POST /mcp (MCP), GET|POST|DELETE /api/subtitles. See the README.\n"))
	})
	mux.HandleFunc("GET /api/subtitles", func(w http.ResponseWriter, r *http.Request) {
		list := wr.man.List(r.URL.Query().Get("match"), r.URL.Query().Get("label"))
		writeJSON(w, http.StatusOK, map[string]any{"count": len(list), "subtitles": list})
	})
	mux.HandleFunc("POST /api/subtitles", func(w http.ResponseWriter, r *http.Request) {
		var q putReq
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSRT+1<<16)).Decode(&q); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad JSON: " + err.Error()})
			return
		}
		e, err := wr.Put(q.Video, q.SRT, q.Label, q.Lang)
		if err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, e)
	})
	mux.HandleFunc("DELETE /api/subtitles", func(w http.ResponseWriter, r *http.Request) {
		e, err := wr.Remove(r.URL.Query().Get("path"))
		if err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"removed": e})
	})
	mux.Handle("POST /mcp", &mcpServer{w: wr})
	mux.HandleFunc("GET /mcp", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Allow", "POST")
		http.Error(w, "this MCP endpoint answers POST requests only", http.StatusMethodNotAllowed)
	})
	return mux
}
