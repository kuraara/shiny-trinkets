package main

// The worker: scans on a schedule, keeps the last few snapshots in DATA, and serves them.
//
//	GET  /healthz                    ok
//	GET  /api/status                 host, whether a scan is running (and how far), last scan, next scan
//	GET  /api/snapshots              the kept snapshots, newest first, with their stats
//	GET  /api/snapshots/latest       the newest snapshot (gzip-compressed ncdu JSON)
//	GET  /api/snapshots/{name}       one snapshot by name
//	POST /api/scan                   start a scan now (if none is running)
//
// With TOKEN set, the snapshot and scan calls need "Authorization: Bearer <TOKEN>" (or ?token=):
// snapshots list every folder name on the machine.

import (
	"compress/gzip"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Config struct {
	Label     string // which machine this is, e.g. "pi"
	Root      string
	RootName  string
	Data      string
	Addr      string
	Token     string
	ScanAt    string          // HH:MM local time
	ScanDays  map[string]bool // empty = every day; else e.g. sun, wed
	MaxAge    time.Duration   // scan at start-up if the newest snapshot is older than this
	Keep      int             // snapshots kept
	KeepFiles int
	MinFile   int64
	Exclude   []string
	AllowFS   []string
}

func configFromEnv() Config {
	host, _ := os.Hostname()
	c := Config{
		Label:     envStr("LABEL", host),
		Root:      envStr("SCAN_ROOT", "/"),
		RootName:  envStr("ROOT_NAME", "/"),
		Data:      envStr("DATA", "/data"),
		Addr:      envStr("ADDR", ":8080"),
		Token:     os.Getenv("TOKEN"),
		ScanAt:    envStr("SCAN_AT", "03:30"),
		ScanDays:  map[string]bool{},
		MaxAge:    time.Duration(envInt("MAX_AGE_HOURS", 26)) * time.Hour,
		Keep:      envInt("KEEP", 8),
		KeepFiles: envInt("KEEP_FILES", 20),
		MinFile:   int64(envInt("MIN_FILE_MB", 100)) << 20,
		Exclude:   splitList(os.Getenv("EXCLUDE")),
		AllowFS:   splitList(os.Getenv("ALLOW_FS")),
	}
	for _, d := range splitList(strings.ToLower(os.Getenv("SCAN_DAYS"))) {
		if len(d) >= 3 {
			c.ScanDays[d[:3]] = true
		}
	}
	return c
}

// Meta sits next to each snapshot as <name>.meta.json.
type Meta struct {
	Name  string `json:"name"` // file name of the snapshot
	Label string `json:"label"`
	Bytes int64  `json:"bytes"` // snapshot file size (compressed)
	Stats
}

type worker struct {
	cfg  Config
	mu   sync.Mutex
	busy bool
	prog *Progress
	from time.Time
	next time.Time
}

func serve(cfg Config) error {
	if err := os.MkdirAll(cfg.Data, 0o755); err != nil {
		return err
	}
	w := &worker{cfg: cfg}
	go w.schedule()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(rw http.ResponseWriter, _ *http.Request) { rw.Write([]byte("ok\n")) })
	mux.HandleFunc("GET /api/status", w.status)
	mux.HandleFunc("GET /api/snapshots", w.auth(w.list))
	mux.HandleFunc("GET /api/snapshots/{name}", w.auth(w.download))
	mux.HandleFunc("POST /api/scan", w.auth(func(rw http.ResponseWriter, _ *http.Request) {
		if !w.start("requested") {
			http.Error(rw, "a scan is already running", http.StatusConflict)
			return
		}
		rw.WriteHeader(http.StatusAccepted)
		rw.Write([]byte("started\n"))
	}))
	log.Printf("diskscan %s: %s on %s, scanning %s at %s, data in %s", version, cfg.Label, cfg.Addr, cfg.Root, cfg.ScanAt, cfg.Data)
	return http.ListenAndServe(cfg.Addr, mux)
}

func (w *worker) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		if w.cfg.Token != "" {
			got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if got == "" {
				got = r.URL.Query().Get("token")
			}
			if subtle.ConstantTimeCompare([]byte(got), []byte(w.cfg.Token)) != 1 {
				http.Error(rw, "token needed", http.StatusUnauthorized)
				return
			}
		}
		h(rw, r)
	}
}

// schedule scans at start-up when the newest snapshot is too old, then at ScanAt on ScanDays.
func (w *worker) schedule() {
	metas := w.metas()
	if len(metas) == 0 || time.Since(metas[0].Started) > w.cfg.MaxAge {
		time.Sleep(time.Minute) // let the machine finish booting first
		w.run("start-up")
	}
	for {
		next := w.nextRun(time.Now())
		w.mu.Lock()
		w.next = next
		w.mu.Unlock()
		time.Sleep(time.Until(next))
		w.run("scheduled")
	}
}

func (w *worker) nextRun(now time.Time) time.Time {
	var hh, mm int
	if _, err := fmt.Sscanf(w.cfg.ScanAt, "%d:%d", &hh, &mm); err != nil {
		hh, mm = 3, 30
	}
	t := time.Date(now.Year(), now.Month(), now.Day(), hh, mm, 0, 0, now.Location())
	for !t.After(now) || (len(w.cfg.ScanDays) > 0 && !w.cfg.ScanDays[strings.ToLower(t.Weekday().String()[:3])]) {
		t = t.AddDate(0, 0, 1)
	}
	return t
}

// start runs a scan in the background; false if one is already running.
func (w *worker) start(why string) bool {
	w.mu.Lock()
	busy := w.busy
	w.mu.Unlock()
	if busy {
		return false
	}
	go w.run(why)
	return true
}

func (w *worker) run(why string) {
	w.mu.Lock()
	if w.busy {
		w.mu.Unlock()
		return
	}
	w.busy, w.prog, w.from = true, &Progress{}, time.Now()
	prog := w.prog
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		w.busy = false
		w.mu.Unlock()
	}()

	name := fmt.Sprintf("%s-%s.json.gz", w.cfg.Label, time.Now().Format("20060102-150405"))
	log.Printf("scan (%s): %s → %s", why, w.cfg.Root, name)
	meta, err := w.scanTo(name, prog)
	if err != nil {
		log.Printf("scan failed: %v", err)
		return
	}
	log.Printf("scan done in %.0fs: %d dirs, %d files, %.1f GB used, %d unreadable, skipped %d mounts",
		meta.Seconds, meta.Dirs, meta.Files, float64(meta.DiskUsage)/1e9, meta.Errors, len(meta.Skipped))
	w.prune()
}

func (w *worker) scanTo(name string, prog *Progress) (*Meta, error) {
	path := filepath.Join(w.cfg.Data, name)
	tmp := path + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp) // no-op after the rename
	gz, _ := gzip.NewWriterLevel(f, gzip.BestSpeed)
	st, err := Scan(Options{
		Root: w.cfg.Root, RootName: w.cfg.RootName, KeepFiles: w.cfg.KeepFiles, MinFile: w.cfg.MinFile,
		Exclude: w.cfg.Exclude, AllowFS: w.cfg.AllowFS,
	}, gz, prog)
	if err == nil {
		err = gz.Close()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return nil, err
	}
	fi, _ := os.Stat(path)
	meta := &Meta{Name: name, Label: w.cfg.Label, Bytes: fi.Size(), Stats: *st}
	b, _ := json.MarshalIndent(meta, "", "  ")
	return meta, os.WriteFile(strings.TrimSuffix(path, ".json.gz")+".meta.json", b, 0o644)
}

// metas lists the kept snapshots, newest first.
func (w *worker) metas() []Meta {
	files, _ := filepath.Glob(filepath.Join(w.cfg.Data, "*.meta.json"))
	var out []Meta
	for _, f := range files {
		var m Meta
		if b, err := os.ReadFile(f); err == nil && json.Unmarshal(b, &m) == nil {
			if _, err := os.Stat(filepath.Join(w.cfg.Data, m.Name)); err == nil {
				out = append(out, m)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	return out
}

func (w *worker) prune() {
	for i, m := range w.metas() {
		if i >= w.cfg.Keep {
			os.Remove(filepath.Join(w.cfg.Data, m.Name))
			os.Remove(filepath.Join(w.cfg.Data, strings.TrimSuffix(m.Name, ".json.gz")+".meta.json"))
		}
	}
}

func (w *worker) status(rw http.ResponseWriter, _ *http.Request) {
	w.mu.Lock()
	st := map[string]any{"label": w.cfg.Label, "version": version, "root": w.cfg.RootName,
		"scanning": w.busy, "nextScan": w.next}
	if w.busy && w.prog != nil {
		st["progress"] = map[string]any{"started": w.from, "dirs": w.prog.Dirs.Load(),
			"files": w.prog.Files.Load(), "bytes": w.prog.Bytes.Load()}
	}
	w.mu.Unlock()
	if m := w.metas(); len(m) > 0 {
		st["last"] = m[0]
	}
	writeJSON(rw, st)
}

func (w *worker) list(rw http.ResponseWriter, _ *http.Request) {
	m := w.metas()
	if m == nil {
		m = []Meta{}
	}
	writeJSON(rw, m)
}

func (w *worker) download(rw http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "latest" {
		m := w.metas()
		if len(m) == 0 {
			http.Error(rw, "no snapshot yet", http.StatusNotFound)
			return
		}
		name = m[0].Name
	}
	if name != filepath.Base(name) || !strings.HasSuffix(name, ".json.gz") {
		http.Error(rw, "bad name", http.StatusBadRequest)
		return
	}
	path := filepath.Join(w.cfg.Data, name)
	if _, err := os.Stat(path); err != nil {
		http.NotFound(rw, r)
		return
	}
	rw.Header().Set("Content-Type", "application/gzip")
	rw.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	http.ServeFile(rw, r, path)
}

func writeJSON(rw http.ResponseWriter, v any) {
	rw.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(rw)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}
