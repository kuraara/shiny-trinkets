// Plan Runner: takes a build plan for a git repository over MCP, has an agent carry it out in a
// fresh clone (one job at a time), pushes the commits and keeps a log. One image, two kinds of
// instance: EXECUTOR=claude (Claude Code) or EXECUTOR=codex (Codex CLI with an OpenAI key), or
// EXECUTOR=command for a plain shell command. Standard library only.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"
)

//go:embed web
var webFiles embed.FS

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func configFromEnv() Config {
	mins, _ := strconv.Atoi(envOr("TIME_LIMIT_MINUTES", "120"))
	return Config{
		Data: envOr("DATA", "/data"), Executor: envOr("EXECUTOR", "claude"), Command: os.Getenv("EXECUTOR_COMMAND"),
		TimeLimit: time.Duration(max(mins, 1)) * time.Minute,
		GitToken:  envOr("GIT_TOKEN", os.Getenv("GH_TOKEN")),
		GitName:   envOr("GIT_NAME", "Plan Runner"), GitEmail: envOr("GIT_EMAIL", "plan-runner@localhost"),
		AskMeURL: os.Getenv("ASKME_URL"), AskMeToken: os.Getenv("ASKME_TOKEN"),
	}
}

func main() {
	cfg := configFromEnv()
	r, err := newRunner(cfg)
	if err != nil {
		log.Fatal(err)
	}
	go r.Work(context.Background())
	token := os.Getenv("TOKEN")
	if token == "" {
		log.Printf("no TOKEN set: anyone who can reach this can submit plans")
	}
	addr := envOr("ADDR", ":8080")
	srv := &http.Server{Addr: addr, Handler: withAuth(token, routes(r)), ReadHeaderTimeout: 10 * time.Second}
	log.Printf("plan-runner (%s executor, %s per job) listening on %s", cfg.Executor, cfg.TimeLimit, addr)
	log.Fatal(srv.ListenAndServe())
}

func routes(r *runner) http.Handler {
	web, _ := fs.Sub(webFiles, "web")
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServerFS(web))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok\n")) })
	mux.HandleFunc("GET /api/info", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"executor": r.cfg.Executor, "timeLimitMinutes": int(r.cfg.TimeLimit.Minutes()), "askMe": r.cfg.AskMeURL != ""})
	})
	mux.HandleFunc("GET /api/jobs", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"jobs": r.List()})
	})
	mux.HandleFunc("POST /api/jobs", func(w http.ResponseWriter, req *http.Request) {
		var b struct {
			Repo, Branch, Title, Plan string
			Push                      *bool
		}
		if json.NewDecoder(http.MaxBytesReader(w, req.Body, 1<<20)).Decode(&b) != nil {
			writeJSON(w, 400, map[string]string{"error": "send JSON: repo, plan, and optionally branch, title, push"})
			return
		}
		j, err := r.Submit(b.Repo, b.Branch, b.Title, b.Plan, b.Push == nil || *b.Push)
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 201, map[string]any{"job": j})
	})
	mux.HandleFunc("GET /api/jobs/{id}", func(w http.ResponseWriter, req *http.Request) {
		j, ok := r.Get(req.PathValue("id"))
		if !ok {
			writeJSON(w, 404, map[string]string{"error": "no job with that id"})
			return
		}
		plan, _ := os.ReadFile(r.dir(j.ID) + "/plan.md")
		writeJSON(w, 200, map[string]any{"job": j, "plan": string(plan), "position": r.Position(j.ID)})
	})
	mux.HandleFunc("GET /api/jobs/{id}/log", func(w http.ResponseWriter, req *http.Request) {
		from, _ := strconv.Atoi(req.URL.Query().Get("from"))
		lines, total := r.Log(req.PathValue("id"), from, 2000)
		writeJSON(w, 200, map[string]any{"lines": lines, "total": total})
	})
	mux.HandleFunc("POST /api/jobs/{id}/cancel", func(w http.ResponseWriter, req *http.Request) {
		j, err := r.Cancel(req.PathValue("id"))
		if err != nil {
			writeJSON(w, 404, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"job": j})
	})
	mux.Handle("POST /mcp", &mcpServer{r: r})
	mux.HandleFunc("GET /mcp", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Allow", "POST")
		http.Error(w, "this MCP endpoint answers POST requests only", http.StatusMethodNotAllowed)
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
