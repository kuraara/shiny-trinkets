package main

// Scene clips for Quote Finder: GET /clip?path=<library path>&start=<s>&end=<s> streams that
// stretch of the file as Matroska, copied without re-encoding (so the Pi does almost no work).
// A copy can only start on a keyframe, so the clip starts at the keyframe at or before `start`;
// the X-Clip-Start header says where that is (seconds into the file) so callers can line up
// subtitle times. Only the main video track and one audio track (English if there is one) are
// kept. Needs the same Bearer token as the index.

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const maxClip = 5 * time.Minute

func (c cfg) serveClips(addr string) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintln(w, "ok") })
	mux.HandleFunc("GET /clip", c.clip)
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("scene clips on %s", addr)
	log.Fatal(srv.ListenAndServe())
}

func (c cfg) clip(w http.ResponseWriter, r *http.Request) {
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if subtle.ConstantTimeCompare([]byte(got), []byte(c.token)) != 1 {
		http.Error(w, "bad token", http.StatusUnauthorized)
		return
	}
	q := r.URL.Query()
	rel := q.Get("path")
	start, err1 := strconv.ParseFloat(q.Get("start"), 64)
	end, err2 := strconv.ParseFloat(q.Get("end"), 64)
	if err1 != nil || err2 != nil || start < 0 || end <= start || end-start > maxClip.Seconds() {
		http.Error(w, fmt.Sprintf("start and end are seconds, end after start, at most %s apart", maxClip), http.StatusBadRequest)
		return
	}
	abs, ok := c.libraryPath(rel)
	if !ok {
		http.Error(w, "no such file in the library", http.StatusNotFound)
		return
	}
	key := keyframeBefore(r, abs, start)
	audio := audioTrack(r, abs)
	args := []string{"-nostdin", "-hide_banner", "-v", "error", "-ss", ff3(key), "-i", abs, "-t", ff3(end - key),
		"-map", "0:v:0"}
	if audio >= 0 {
		args = append(args, "-map", "0:"+strconv.Itoa(audio))
	}
	args = append(args, "-c", "copy", "-sn", "-dn", "-map_metadata", "-1", "-map_chapters", "-1", "-f", "matroska", "pipe:1")
	cmd := exec.CommandContext(r.Context(), "nice", append([]string{"-n", "10", "ffmpeg"}, args...)...)
	cmd.Stdout = w
	var stderr strings.Builder
	cmd.Stderr = &stderr
	w.Header().Set("Content-Type", "video/x-matroska")
	w.Header().Set("X-Clip-Start", ff3(key))
	t0 := time.Now()
	if err := cmd.Run(); err != nil {
		log.Printf("clip %s %.1f-%.1f: %v %s", rel, start, end, err, firstLine(fmt.Errorf("%s", stderr.String())))
		return // headers are gone; the caller sees a short or empty body
	}
	log.Printf("clip %s %.1f-%.1f (from keyframe %.2f) in %s", rel, start, end, key, time.Since(t0).Round(time.Millisecond))
}

// libraryPath resolves a library-relative path, refusing anything outside the media folder.
func (c cfg) libraryPath(rel string) (string, bool) {
	if rel == "" || strings.Contains(rel, "\x00") || filepath.IsAbs(rel) {
		return "", false
	}
	abs := filepath.Join(c.media, filepath.FromSlash(rel))
	if !strings.HasPrefix(abs, filepath.Clean(c.media)+string(filepath.Separator)) {
		return "", false
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil || !strings.HasPrefix(real, filepath.Clean(c.media)+string(filepath.Separator)) {
		return "", false
	}
	st, err := os.Stat(real)
	if err != nil || !st.Mode().IsRegular() || !videoExt[strings.ToLower(filepath.Ext(real))] {
		return "", false
	}
	return real, true
}

// keyframeBefore finds the last video keyframe at or before t (reads only a few seconds).
func keyframeBefore(r *http.Request, abs string, t float64) float64 {
	from := max(0, t-20)
	out, err := exec.CommandContext(r.Context(), "ffprobe", "-v", "error", "-select_streams", "v:0", "-skip_frame", "nokey",
		"-show_entries", "frame=pts_time,best_effort_timestamp_time", "-read_intervals", ff3(from)+"%"+ff3(t+0.05),
		"-of", "csv=p=0", abs).Output()
	best := -1.0
	if err == nil {
		for _, l := range strings.Split(string(out), "\n") {
			for _, f := range strings.Split(l, ",") {
				if v, err := strconv.ParseFloat(strings.TrimSpace(f), 64); err == nil && v <= t+0.001 && v > best {
					best = v
				}
			}
		}
	}
	if best < 0 {
		return from // no keyframe found: start a little early; times stay roughly right
	}
	return best
}

// audioTrack picks the English (else default, else first) audio stream; -1 if none.
func audioTrack(r *http.Request, abs string) int {
	out, err := exec.CommandContext(r.Context(), "ffprobe", "-v", "error", "-select_streams", "a",
		"-show_entries", "stream=index:stream_tags=language:stream_disposition=default", "-of", "json", abs).Output()
	if err != nil {
		return -1
	}
	var p struct{ Streams []stream }
	if json.Unmarshal(out, &p) != nil || len(p.Streams) == 0 {
		return -1
	}
	best, bestScore := p.Streams[0].Index, -1
	for _, s := range p.Streams {
		n := 0
		switch strings.ToLower(s.Tags["language"]) {
		case "eng", "en":
			n += 10
		case "", "und":
			n += 3
		}
		if s.Disposition["default"] == 1 {
			n += 2
		}
		if n > bestScore {
			best, bestScore = s.Index, n
		}
	}
	return best
}

func ff3(f float64) string { return strconv.FormatFloat(f, 'f', 3, 64) }
