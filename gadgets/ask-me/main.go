// Ask Me: a place for agents to leave a question for the owner when they can't get an answer in
// their own session. Agents ask through MCP (/mcp); the owner answers on the page; the answer goes
// back to the agent, which can wait for it or check later. Standard library only.
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
	addr := envOr("ADDR", ":8080")
	st, err := openStore(envOr("DATA", "/data") + "/questions.json")
	if err != nil {
		log.Fatal(err)
	}
	srv := &http.Server{Addr: addr, Handler: routes(st), ReadHeaderTimeout: 10 * time.Second}
	log.Printf("ask-me listening on %s", addr)
	log.Fatal(srv.ListenAndServe())
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func routes(st *store) http.Handler {
	web, _ := fs.Sub(webFiles, "web")
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServerFS(web))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok\n")) })
	mux.HandleFunc("GET /api/questions", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"questions": st.List()})
	})
	mux.HandleFunc("POST /api/questions/{id}/answer", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Answer string `json:"answer"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body) != nil || strings.TrimSpace(body.Answer) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Type an answer or tap one of the suggestions."})
			return
		}
		q, err := st.Answer(r.PathValue("id"), body.Answer)
		finishReply(w, q, err)
	})
	mux.HandleFunc("POST /api/questions/{id}/dismiss", func(w http.ResponseWriter, r *http.Request) {
		q, err := st.Withdraw(r.PathValue("id"))
		finishReply(w, q, err)
	})
	mux.Handle("POST /mcp", &mcpServer{st: st})
	mux.HandleFunc("GET /mcp", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Allow", "POST")
		http.Error(w, "this MCP endpoint answers POST requests only", http.StatusMethodNotAllowed)
	})
	return mux
}

func finishReply(w http.ResponseWriter, q Question, err error) {
	switch {
	case err == errNotOpen:
		writeJSON(w, http.StatusConflict, map[string]any{"error": "That question was already answered or withdrawn.", "question": q})
	case err != nil:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"question": q})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
