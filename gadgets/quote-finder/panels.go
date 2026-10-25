package main

// "Make panels" / "Make clip": turn a stretch of dialogue into a Panel Script job, or a video
// clip (optionally with the subtitles burned in), in video-tools.
//
//  1. the subtitle lines from the first to the last chosen line come from the index;
//  2. the scene is cut from the media file by the extractor next to the media (stream copy,
//     no re-encoding; it starts on the keyframe before the first line and says where);
//  3. the clip streams straight into a video-tools upload (never held in memory), and a panel
//     job starts with the subtitle lines, re-timed to the clip, so no speech recognition runs;
//  4. the user (or an agent, over video-tools' MCP) names the speakers on its review page.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	maxPanelLines = 60
	maxPanelSpan  = 5 * 60 * 1000 // ms
	leadIn        = 400           // ms of picture before the first line
	tailOut       = 600           // ms after the last line
)

type panelCfg struct {
	clipURL     string // extractor on the Pi, e.g. http://<pi>:8105
	videoTools  string // video-tools as this server reaches it
	publicTools string // video-tools as the user's browser reaches it
	toolsToken  string // video-tools AUTH_TOKEN, if it has one
}

func (p panelCfg) enabled() bool { return p.clipURL != "" && p.videoTools != "" }

type panelResult struct {
	JobID    string `json:"jobId"`
	URL      string `json:"url"`
	ToolsURL string `json:"toolsUrl"`
	Lines    int    `json:"lines"`
	Title    string `json:"title"`
	Clip     string `json:"clip"`
	Status   string `json:"status"`
}

type sceneLine struct {
	Start, End int
	Text       string
}

// sceneLines returns the media file and the subtitle lines whose start is between from and to.
func (s *store) sceneLines(mediaID int64, from, to int) (string, hit, []sceneLine, error) {
	var path string
	var h hit
	err := s.db.QueryRow(`SELECT path, kind, title, year, season, episode, episode2, ep_title FROM media WHERE id = ?`, mediaID).
		Scan(&path, &h.Kind, &h.Title, &h.Year, &h.Season, &h.Episode, &h.Episode2, &h.EpTitle)
	if errors.Is(err, sql.ErrNoRows) {
		return "", h, nil, errors.New("no such episode or movie")
	} else if err != nil {
		return "", h, nil, err
	}
	rows, err := s.db.Query(`SELECT start_ms, end_ms, text FROM cues WHERE media_id = ? AND start_ms BETWEEN ? AND ? ORDER BY idx LIMIT ?`,
		mediaID, from, to, maxPanelLines+1)
	if err != nil {
		return "", h, nil, err
	}
	defer rows.Close()
	var ls []sceneLine
	for rows.Next() {
		var l sceneLine
		if err := rows.Scan(&l.Start, &l.End, &l.Text); err != nil {
			return "", h, nil, err
		}
		ls = append(ls, l)
	}
	return path, h, ls, rows.Err()
}

var unsafeName = regexp.MustCompile(`[^\p{L}\p{N} !'&(),.\-]+`)

func clipName(h hit, from int) string {
	n := h.Title
	if h.Kind == "tv" {
		n += fmt.Sprintf(" S%02dE%02d", h.Season, h.Episode)
	} else if h.Year > 0 {
		n += fmt.Sprintf(" (%d)", h.Year)
	}
	s := from / 1000
	n += fmt.Sprintf(" %dm%02ds", s/60, s%60)
	return strings.TrimSpace(unsafeName.ReplaceAllString(n, " ")) + ".mkv"
}

func (a *app) makePanels(ctx context.Context, mediaID int64, from, to int) (panelResult, error) {
	return a.makeScene(ctx, mediaID, from, to, sceneOpts{})
}

// sceneOpts: Clip makes a video clip instead of panels; Subtitles burns the lines into it.
type sceneOpts struct{ Clip, Subtitles bool }

func (a *app) makeScene(ctx context.Context, mediaID int64, from, to int, o sceneOpts) (panelResult, error) {
	var res panelResult
	if !a.panels.enabled() {
		return res, errors.New("making panels isn't set up (CLIP_URL and VIDEO_TOOLS_URL)")
	}
	if to < from {
		from, to = to, from
	}
	path, h, lines, err := a.db.sceneLines(mediaID, from, to)
	if err != nil {
		return res, err
	}
	switch {
	case len(lines) == 0:
		return res, errors.New("no lines in that range")
	case len(lines) > maxPanelLines:
		return res, fmt.Errorf("that's more than %d lines; pick a shorter stretch", maxPanelLines)
	case lines[len(lines)-1].End-lines[0].Start > maxPanelSpan:
		return res, errors.New("that's longer than 5 minutes; pick a shorter stretch")
	}
	lead, tail := leadIn, tailOut
	if o.Clip { // a clip breathes a little either side
		lead, tail = 1000, 1200
	}
	start := max(0, lines[0].Start-lead)
	end := lines[len(lines)-1].End + tail
	name := clipName(h, lines[0].Start)

	// 1. the scene, from the extractor next to the media
	q := url.Values{"path": {path}, "start": {msec(start)}, "end": {msec(end)}}
	req, _ := http.NewRequestWithContext(ctx, "GET", a.panels.clipURL+"/clip?"+q.Encode(), nil)
	req.Header.Set("Authorization", "Bearer "+a.token)
	clip, err := http.DefaultClient.Do(req)
	if err != nil {
		return res, fmt.Errorf("couldn't reach the media server: %w", err)
	}
	defer clip.Body.Close()
	if clip.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(clip.Body, 300))
		return res, fmt.Errorf("media server: %s", strings.TrimSpace(string(b)))
	}
	key, err := strconv.ParseFloat(clip.Header.Get("X-Clip-Start"), 64)
	if err != nil {
		return res, errors.New("media server didn't say where the clip starts")
	}

	// 2. streamed into a video-tools upload
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		part, err := mw.CreateFormFile("file", name)
		if err == nil {
			var n int64
			n, err = io.Copy(part, clip.Body)
			if err == nil && n < 1024 {
				err = errors.New("the media server sent an empty clip")
			}
		}
		if err == nil {
			err = mw.Close()
		}
		pw.CloseWithError(err)
	}()
	var up struct {
		Files []struct {
			ID string `json:"id"`
		} `json:"files"`
		Error string `json:"error"`
	}
	if err := a.toolsCall(ctx, "POST", "/api/files", mw.FormDataContentType(), pr, &up); err != nil {
		return res, err
	}
	if len(up.Files) == 0 {
		return res, errors.New("video-tools didn't take the clip")
	}

	// 3. the panel job, with the subtitles re-timed to the clip
	gl := make([]map[string]any, len(lines))
	for i, l := range lines {
		gl[i] = map[string]any{"text": l.Text, "start": round2(float64(l.Start)/1000 - key), "end": round2(float64(l.End)/1000 - key)}
	}
	endpoint, req2 := "/api/panel", map[string]any{"file_id": up.Files[0].ID, "lines": gl}
	if o.Clip {
		endpoint = "/api/clip"
		req2 = map[string]any{"file_id": up.Files[0].ID, "start": max(0, round2(float64(start)/1000-key)), "end": round2(float64(end)/1000 - key),
			"name": strings.TrimSuffix(name, ".mkv"), "delete_input": true}
		if o.Subtitles {
			req2["subtitles"] = gl
		}
	}
	body, _ := json.Marshal(req2)
	var job struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := a.toolsCall(ctx, "POST", endpoint, "application/json", strings.NewReader(string(body)), &job); err != nil {
		return res, err
	}
	pub := a.panels.publicTools
	if pub == "" {
		pub = a.panels.videoTools
	}
	link := pub + "/jobs/" + job.ID
	if !o.Clip {
		link = "/panel.html?job=" + job.ID // Quote Finder's own panel editor
	}
	return panelResult{JobID: job.ID, URL: link, ToolsURL: pub + "/jobs/" + job.ID, Lines: len(lines), Title: strings.TrimSuffix(name, ".mkv"),
		Clip: fmt.Sprintf("%s–%s", msec(start), msec(end)), Status: job.Status}, nil
}

func (a *app) toolsCall(ctx context.Context, method, path, ctype string, body io.Reader, out any) error {
	req, _ := http.NewRequestWithContext(ctx, method, a.panels.videoTools+path, body)
	req.Header.Set("Content-Type", ctype)
	if a.panels.toolsToken != "" {
		req.Header.Set("Authorization", "Bearer "+a.panels.toolsToken)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("couldn't reach video-tools: %w", err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if res.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		json.Unmarshal(b, &e)
		if e.Error == "" {
			e.Error = res.Status
		}
		return fmt.Errorf("video-tools: %s", e.Error)
	}
	return json.Unmarshal(b, out)
}

func (a *app) apiPanels(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Media     int64 `json:"media"`
		From, To  int
		Clip      bool `json:"clip"`
		Subtitles bool `json:"subtitles"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad body"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	res, err := a.makeScene(ctx, req.Media, req.From, req.To, sceneOpts{Clip: req.Clip, Subtitles: req.Subtitles})
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func msec(ms int) string       { return strconv.FormatFloat(float64(ms)/1000, 'f', 3, 64) }
func round2(f float64) float64 { return float64(int(f*100+0.5)) / 100 }

// ---- the panel editor reads and saves video-tools panel jobs through these (same origin, so the
// browser can draw the frames on a canvas and export it)

var jobIDRe = regexp.MustCompile(`^j_[a-z0-9]{6,40}$`)

// okFileName accepts a job output's name as video-tools makes them (clip names have spaces,
// "!" and the like) but never anything that could step out of the job's folder.
func okFileName(n string) bool {
	return n != "" && len(n) <= 200 && !strings.ContainsAny(n, "/\\\x00") && !strings.HasPrefix(n, ".")
}

func (a *app) vtJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !jobIDRe.MatchString(id) || !a.panels.enabled() {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such job"})
		return
	}
	a.vtProxy(w, r, "GET", "/api/jobs/"+id, nil)
}

func (a *app) vtFile(w http.ResponseWriter, r *http.Request) {
	id, name := r.PathValue("id"), r.PathValue("name")
	if !jobIDRe.MatchString(id) || !okFileName(name) || !a.panels.enabled() {
		http.NotFound(w, r)
		return
	}
	a.vtProxy(w, r, r.Method, "/jobs/"+id+"/files/"+url.PathEscape(name), nil)
}

func (a *app) vtRender(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !jobIDRe.MatchString(id) || !a.panels.enabled() {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such job"})
		return
	}
	a.vtProxy(w, r, "POST", "/api/jobs/"+id+"/render", http.MaxBytesReader(w, r.Body, 256<<10))
}

func (a *app) vtProxy(w http.ResponseWriter, r *http.Request, method, path string, body io.Reader) {
	ctx := r.Context() // files can be big videos: no time limit beyond the visitor's connection
	if body != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
	}
	req, _ := http.NewRequestWithContext(ctx, method, a.panels.videoTools+path, body)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// byte ranges: iPhones only play video from a server that answers them
	for _, h := range []string{"Range", "If-Range", "If-None-Match", "If-Modified-Since"} {
		if v := r.Header.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}
	if a.panels.toolsToken != "" {
		req.Header.Set("Authorization", "Bearer "+a.panels.toolsToken)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "couldn't reach video-tools"})
		return
	}
	defer res.Body.Close()
	for _, h := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "Last-Modified", "ETag"} {
		if v := res.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(res.StatusCode)
	io.Copy(w, res.Body)
}
