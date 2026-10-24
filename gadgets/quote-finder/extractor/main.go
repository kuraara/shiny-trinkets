// extractor runs next to the media (on the Pi). It walks the library, pulls the best English
// text subtitle track out of each video with ffmpeg, and sends it to Quote Finder. It reads
// only, one file at a time, at the lowest CPU and disk priority.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type cfg struct {
	media, url, token string
	only              []string
	rescan            time.Duration
}

var videoExt = map[string]bool{".mkv": true, ".mp4": true, ".m4v": true, ".avi": true, ".mov": true, ".webm": true, ".ts": true}

func main() {
	c := cfg{
		media:  env("MEDIA_DIR", "/media"),
		url:    strings.TrimRight(env("INDEX_URL", "http://<pc>:8104"), "/"),
		token:  os.Getenv("INGEST_TOKEN"),
		rescan: time.Duration(atoi(env("RESCAN_HOURS", "6"))) * time.Hour,
	}
	for _, o := range strings.Split(os.Getenv("ONLY"), ",") {
		if o = strings.Trim(strings.TrimSpace(o), "/"); o != "" {
			c.only = append(c.only, o)
		}
	}
	if c.token == "" {
		log.Fatal("INGEST_TOKEN is required")
	}
	log.Printf("extractor: media %s → %s, only %v, rescan every %s", c.media, c.url, c.only, c.rescan)
	for {
		if err := pass(c); err != nil {
			log.Printf("pass failed: %v (retrying in 5 minutes)", err)
			time.Sleep(5 * time.Minute)
			continue
		}
		if c.rescan <= 0 {
			return
		}
		time.Sleep(c.rescan)
	}
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

type known struct {
	Size   int64  `json:"size"`
	Mtime  int64  `json:"mtime"`
	Status string `json:"status"`
}

func (c cfg) inScope(rel string) bool {
	if len(c.only) == 0 {
		return true
	}
	for _, o := range c.only {
		if rel == o || strings.HasPrefix(rel, o+"/") {
			return true
		}
	}
	return false
}

func pass(c cfg) error {
	var have map[string]known
	if err := c.call("GET", "/api/ingest/known", nil, &have); err != nil {
		return err
	}
	type file struct {
		rel   string
		size  int64
		mtime int64
	}
	var todo []file
	seen := map[string]bool{}
	err := filepath.WalkDir(c.media, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable folder: skip it
		}
		rel, _ := filepath.Rel(c.media, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel != "." && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !videoExt[strings.ToLower(filepath.Ext(p))] || strings.HasPrefix(d.Name(), ".") || !c.inScope(rel) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		seen[rel] = true
		k, ok := have[rel]
		if ok && k.Size == info.Size() && k.Mtime == info.ModTime().Unix() && k.Status != "error" {
			return nil
		}
		todo = append(todo, file{rel, info.Size(), info.ModTime().Unix()})
		return nil
	})
	if err != nil {
		return err
	}
	// files that have gone from the library
	var gone []string
	for p := range have {
		if c.inScope(p) && !seen[p] {
			gone = append(gone, p)
		}
	}
	if len(gone) > 0 {
		c.call("POST", "/api/ingest/forget", gone, nil)
		log.Printf("forgot %d removed file(s)", len(gone))
	}
	sort.Slice(todo, func(i, j int) bool { return todo[i].rel < todo[j].rel })
	log.Printf("%d file(s) to read", len(todo))
	start := time.Now()
	for i, f := range todo {
		item := extract(filepath.Join(c.media, filepath.FromSlash(f.rel)))
		item.Path, item.Size, item.Mtime = f.rel, f.size, f.mtime
		var out struct {
			Lines int `json:"lines"`
		}
		if err := c.call("POST", "/api/ingest", item, &out); err != nil {
			return err
		}
		log.Printf("[%d/%d] %s: %s %s (%d lines)", i+1, len(todo), f.rel, item.Status, item.Source, out.Lines)
	}
	if len(todo) > 0 {
		log.Printf("pass done: %d file(s) in %s", len(todo), time.Since(start).Round(time.Second))
	}
	return nil
}

type ingestItem struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Mtime  int64  `json:"mtime"`
	Status string `json:"status"`
	Source string `json:"source"`
	SRT    string `json:"srt,omitempty"`
}

type stream struct {
	Index       int               `json:"index"`
	Codec       string            `json:"codec_name"`
	Tags        map[string]string `json:"tags"`
	Disposition map[string]int    `json:"disposition"`
}

var textCodecs = map[string]bool{"subrip": true, "ass": true, "ssa": true, "mov_text": true, "webvtt": true, "text": true}

// score ranks a subtitle track: English, full (not forced/signs-only), plain over SDH.
func score(s stream) int {
	lang := strings.ToLower(s.Tags["language"])
	title := strings.ToLower(s.Tags["title"])
	n := 0
	switch lang {
	case "eng", "en", "en-us", "en-gb":
		n += 100
	case "", "und":
		n += 40
	default:
		return -1
	}
	if s.Disposition["forced"] == 1 || strings.Contains(title, "forced") || strings.Contains(title, "sign") || strings.Contains(title, "song") {
		n -= 80
	}
	if s.Disposition["hearing_impaired"] == 1 || strings.Contains(title, "sdh") {
		n -= 5
	}
	if s.Codec == "subrip" {
		n += 2
	}
	return n
}

func extract(path string) ingestItem {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-select_streams", "s",
		"-show_entries", "stream=index,codec_name:stream_tags=language,title:stream_disposition=forced,hearing_impaired",
		"-of", "json", path).Output()
	if err != nil {
		return ingestItem{Status: "error", Source: "ffprobe: " + firstLine(err)}
	}
	var probe struct {
		Streams []stream `json:"streams"`
	}
	json.Unmarshal(out, &probe)
	if len(probe.Streams) == 0 {
		return ingestItem{Status: "none", Source: "no subtitles in the file"}
	}
	best, bestScore, images := stream{}, 0, 0
	for _, s := range probe.Streams {
		if !textCodecs[s.Codec] {
			images++
			continue
		}
		if sc := score(s); sc > bestScore {
			best, bestScore = s, sc
		}
	}
	if bestScore <= 0 {
		if images > 0 {
			return ingestItem{Status: "image", Source: "only picture-based subtitles"}
		}
		return ingestItem{Status: "none", Source: "no English text subtitles"}
	}
	desc := fmt.Sprintf("track %d %s %s %s", best.Index, best.Codec, best.Tags["language"], best.Tags["title"])
	cmd := exec.CommandContext(ctx, "nice", "-n", "19", "ionice", "-c", "3",
		"ffmpeg", "-nostdin", "-v", "error", "-i", path, "-map", "0:"+strconv.Itoa(best.Index), "-f", "srt", "-")
	var buf, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &stderr
	if err := cmd.Run(); err != nil {
		return ingestItem{Status: "error", Source: desc + ": " + firstLine(fmt.Errorf("%v %s", err, stderr.String()))}
	}
	return ingestItem{Status: "indexed", Source: strings.TrimSpace(desc), SRT: buf.String()}
}

func firstLine(err error) string {
	s := strings.TrimSpace(err.Error())
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

func (c cfg) call(method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	for attempt := 1; ; attempt++ {
		req, _ := http.NewRequest(method, c.url+path, body)
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err == nil && res.StatusCode < 300 {
			defer res.Body.Close()
			if out != nil {
				return json.NewDecoder(res.Body).Decode(out)
			}
			return nil
		}
		if err == nil {
			msg, _ := io.ReadAll(io.LimitReader(res.Body, 300))
			res.Body.Close()
			err = fmt.Errorf("%s %s: %d %s", method, path, res.StatusCode, msg)
		}
		if attempt >= 5 {
			return err
		}
		log.Printf("%v (retry %d)", err, attempt)
		time.Sleep(time.Duration(attempt*10) * time.Second)
		if in != nil {
			b, _ := json.Marshal(in)
			body = bytes.NewReader(b)
		}
	}
}
