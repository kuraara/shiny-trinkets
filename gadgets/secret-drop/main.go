// Secret Drop: type a password, API key or token on your phone, or paste a picture, and it
// becomes a file Claude can read (<name>.env and <name>-img-<time>-<n>.<ext> in DIR, mode 0600),
// without a terminal. The page only ever writes: saved values and images are never shown again,
// only each name, the KEY names in its .env, how many images and when it changed.
// Standard library only.
package main

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

//go:embed web
var webFiles embed.FS

var (
	nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,40}$`)
	keyRe  = regexp.MustCompile(`^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=`)
)

const (
	maxSize  = 32 << 10
	maxImage = 25 << 20
	maxBatch = 100 << 20
)

// what a picture may be: sniffed from its bytes, or (for HEIC, which Go can't sniff) its file name
var imageExt = map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp"}

func main() {
	dir := envOr("DIR", "/secrets")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Fatal(err)
	}
	token := os.Getenv("TOKEN")
	if token == "" {
		log.Printf("no TOKEN set: anyone who can reach this can add or delete secrets")
	}
	srv := &http.Server{Addr: envOr("ADDR", ":8080"), Handler: withAuth(token, routes(dir)), ReadHeaderTimeout: 10 * time.Second}
	log.Printf("secret-drop listening on %s, writing to %s", srv.Addr, dir)
	log.Fatal(srv.ListenAndServe())
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

type entry struct {
	Name    string    `json:"name"`
	Keys    []string  `json:"keys"`
	Lines   int       `json:"lines"`
	Images  int       `json:"images"`
	Updated time.Time `json:"updated"`
}

func routes(dir string) http.Handler {
	web, _ := fs.Sub(webFiles, "web")
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServerFS(web))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok\n")) })
	mux.HandleFunc("GET /api/secrets", func(w http.ResponseWriter, _ *http.Request) {
		list, err := listSecrets(dir)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"secrets": list})
	})
	mux.HandleFunc("PUT /api/secrets/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		var body struct {
			Text string `json:"text"`
		}
		if !nameRe.MatchString(name) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Name: lower-case letters, digits and dashes, starting with a letter (like hydrawise)."})
			return
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSize+1024)).Decode(&body); err != nil || len(body.Text) > maxSize {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "That's too long (32 KB at most)."})
			return
		}
		text := strings.TrimSpace(strings.ReplaceAll(body.Text, "\r\n", "\n"))
		if text == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Type something to save."})
			return
		}
		if err := save(dir, name+".env", []byte(text+"\n")); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		log.Printf("saved %s.env (%d keys)", name, len(keysOf(text)))
		writeJSON(w, http.StatusOK, map[string]any{"saved": name + ".env", "keys": keysOf(text)})
	})
	mux.HandleFunc("POST /api/secrets/{name}/images", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if !nameRe.MatchString(name) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Name: lower-case letters, digits and dashes, starting with a letter."})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBatch)
		mr, err := r.MultipartReader()
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Send the pictures as a form upload."})
			return
		}
		stamp := time.Now().Format("20060102-150405")
		var saved []string
		for n := 1; ; n++ {
			part, err := mr.NextPart()
			if errors.Is(err, io.EOF) {
				break
			} else if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "The upload was cut short or too big (100 MB at most)."})
				return
			}
			if part.FormName() != "image" {
				continue
			}
			b, err := io.ReadAll(io.LimitReader(part, maxImage+1))
			if err != nil || len(b) > maxImage {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "A picture is too big (25 MB at most)."})
				return
			}
			ext := imageExt[http.DetectContentType(b)]
			if fe := strings.ToLower(filepath.Ext(part.FileName())); ext == "" && (fe == ".heic" || fe == ".heif") {
				ext = fe
			}
			if ext == "" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": part.FileName() + " isn't a picture (PNG, JPEG, GIF, WebP or HEIC)."})
				return
			}
			file := fmt.Sprintf("%s-img-%s-%d%s", name, stamp, n, ext)
			if err := save(dir, file, b); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			saved = append(saved, file)
		}
		if len(saved) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "No pictures came through."})
			return
		}
		log.Printf("saved %d image(s) for %s", len(saved), name)
		writeJSON(w, http.StatusOK, map[string]any{"saved": saved})
	})
	mux.HandleFunc("DELETE /api/secrets/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if !nameRe.MatchString(name) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no such secret"})
			return
		}
		files := imagesOf(dir, name)
		if _, err := os.Stat(filepath.Join(dir, name+".env")); err == nil {
			files = append(files, filepath.Join(dir, name+".env"))
		}
		if len(files) == 0 {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such secret"})
			return
		}
		for _, f := range files {
			if err := os.Remove(f); err != nil && !errors.Is(err, fs.ErrNotExist) {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
		}
		log.Printf("deleted %s (%d files)", name, len(files))
		writeJSON(w, http.StatusOK, map[string]string{"deleted": name})
	})
	return mux
}

// save writes the file through a temporary one, so a reader never sees half a secret.
func save(dir, file string, data []byte) error {
	tmp, err := os.CreateTemp(dir, "."+file+"-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, file))
}

// imagesOf lists the pictures saved under a name (and not a longer name that starts the same).
func imagesOf(dir, name string) []string {
	files, _ := filepath.Glob(filepath.Join(dir, name+"-img-*"))
	out := []string{}
	for _, f := range files {
		if m := imageFile.FindStringSubmatch(filepath.Base(f)); m != nil && m[1] == name {
			out = append(out, f)
		}
	}
	return out
}

// keysOf lists the KEY names of KEY=value lines (never the values).
func keysOf(text string) []string {
	keys := []string{}
	for _, l := range strings.Split(text, "\n") {
		if m := keyRe.FindStringSubmatch(l); m != nil {
			keys = append(keys, m[1])
		}
	}
	return keys
}

var imageFile = regexp.MustCompile(`^([a-z][a-z0-9-]{0,40})-img-\d{8}-\d{6}-\d+\.[a-z]+$`)

func listSecrets(dir string) ([]entry, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	by := map[string]*entry{}
	get := func(name string) *entry {
		if by[name] == nil {
			by[name] = &entry{Name: name, Keys: []string{}}
		}
		return by[name]
	}
	for _, f := range files {
		info, err := f.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		var e *entry
		if name, ok := strings.CutSuffix(f.Name(), ".env"); ok && nameRe.MatchString(name) {
			b, err := os.ReadFile(filepath.Join(dir, f.Name()))
			if err != nil {
				continue
			}
			text := strings.TrimSpace(string(b))
			e = get(name)
			e.Keys, e.Lines = keysOf(text), len(strings.Split(text, "\n"))
		} else if m := imageFile.FindStringSubmatch(f.Name()); m != nil {
			e = get(m[1])
			e.Images++
		} else {
			continue
		}
		if info.ModTime().After(e.Updated) {
			e.Updated = info.ModTime()
		}
	}
	out := []entry{}
	for _, e := range by {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out, nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
