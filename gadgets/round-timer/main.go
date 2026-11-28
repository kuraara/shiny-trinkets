// Round Timer server: serves round-timer.html unchanged (it also runs on its own from the Files
// app) and keeps the one shared session, so a phone that switches apps or reloads the page lands
// back in the same session, and a second device can follow it.
//
//	GET  /api/state   {"rev":N,"now":ms,"updated":ms,"state":{...}|null}
//	PUT  /api/state   {"rev":<the rev you last saw>,"state":{...}|null}
//	                  200 {"rev":N+1,"now":ms}, or 409 with the current state if someone else wrote first
//
// The state is opaque to the server: the page decides what is in it. Standard library only.
package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

//go:embed round-timer.html
var pageHTML []byte

//go:embed web/icon.svg
var iconSVG []byte

const (
	maxBody  = 256 << 10      // a session is a few KB; this is only a guard
	stateTTL = 24 * time.Hour // a session nobody has touched for this long is forgotten
)

type store struct {
	mu      sync.Mutex
	path    string // "" = memory only
	rev     int64
	state   json.RawMessage // nil = no session
	updated time.Time
	now     func() time.Time
}

type diskState struct {
	Rev     int64           `json:"rev"`
	Updated time.Time       `json:"updated"`
	State   json.RawMessage `json:"state"`
}

func newStore(dir string, now func() time.Time) *store {
	s := &store{now: now}
	if dir == "" {
		return s
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("state folder %s is not usable (%v): keeping state in memory only", dir, err)
		return s
	}
	s.path = filepath.Join(dir, "state.json")
	b, err := os.ReadFile(s.path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("reading %s: %v", s.path, err)
		}
		return s
	}
	var d diskState
	if err := json.Unmarshal(b, &d); err != nil {
		log.Printf("ignoring unreadable %s: %v", s.path, err)
		return s
	}
	s.rev, s.updated = d.Rev, d.Updated
	if len(d.State) > 0 && string(d.State) != "null" {
		s.state = d.State
	}
	return s
}

// snapshot is what GET returns. The revision survives expiry, so clients never go backwards.
func (s *store) snapshot() map[string]any {
	st := s.state
	if st != nil && s.now().Sub(s.updated) > stateTTL {
		st = nil
	}
	var state any
	if st != nil {
		state = st
	}
	return map[string]any{"rev": s.rev, "now": s.now().UnixMilli(), "updated": s.updated.UnixMilli(), "state": state}
}

func (s *store) persist() {
	if s.path == "" {
		return
	}
	b, _ := json.Marshal(diskState{Rev: s.rev, Updated: s.updated, State: s.state})
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		log.Printf("saving state: %v", err)
		return
	}
	if err := os.Rename(tmp, s.path); err != nil {
		log.Printf("saving state: %v", err)
	}
}

func (s *store) get() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshot()
}

// put stores state if base is the current revision; otherwise it returns the current one (ok=false).
func (s *store) put(base int64, state json.RawMessage) (map[string]any, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if base != s.rev {
		out := s.snapshot()
		out["conflict"] = true
		return out, false
	}
	s.rev++
	s.updated = s.now()
	s.state = state
	s.persist()
	return map[string]any{"rev": s.rev, "now": s.now().UnixMilli()}, true
}

func main() {
	addr := ":8080"
	if a := os.Getenv("ADDR"); a != "" {
		addr = a
	}
	dir := "/data"
	if d, ok := os.LookupEnv("DATA_DIR"); ok {
		dir = d
	}
	srv := &http.Server{Addr: addr, Handler: routes(newStore(dir, time.Now)), ReadHeaderTimeout: 10 * time.Second}
	log.Printf("round-timer listening on %s (state in %q)", addr, dir)
	log.Fatal(srv.ListenAndServe())
}

func routes(s *store) http.Handler {
	mux := http.NewServeMux()
	page := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(pageHTML)
	}
	mux.HandleFunc("GET /{$}", page)
	mux.HandleFunc("GET /round-timer.html", page)
	mux.HandleFunc("GET /icon.svg", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(iconSVG)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok\n")) })
	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, s.get())
	})
	mux.HandleFunc("PUT /api/state", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		var in struct {
			Rev   *int64          `json:"rev"`
			State json.RawMessage `json:"state"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "that state is too big"})
				return
			}
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "send JSON like {\"rev\":0,\"state\":{...}}"})
			return
		}
		if in.Rev == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "\"rev\" is the revision you last saw"})
			return
		}
		state := bytes.TrimSpace(in.State)
		if len(state) == 0 || string(state) == "null" {
			state = nil // clears the session
		} else if state[0] != '{' || !json.Valid(state) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "\"state\" must be an object or null"})
			return
		}
		out, ok := s.put(*in.Rev, state)
		status := http.StatusOK
		if !ok {
			status = http.StatusConflict
		}
		writeJSON(w, status, out)
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
