// Package web serves the browser UI, the JSON API and mounts the MCP endpoint.
package web

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"

	videotools "github.com/kuraara/clowd-docker-stuff/video-tools"
	"github.com/kuraara/clowd-docker-stuff/video-tools/internal/app"
	"github.com/kuraara/clowd-docker-stuff/video-tools/internal/jobs"
	"github.com/kuraara/clowd-docker-stuff/video-tools/internal/panel"
	"github.com/kuraara/clowd-docker-stuff/video-tools/internal/storage"
	"github.com/kuraara/clowd-docker-stuff/video-tools/internal/web/views"
)

type Server struct {
	svc *app.Service
}

// New builds the full HTTP handler. mcpHandler is mounted at /mcp.
func New(svc *app.Service, mcpHandler http.Handler) http.Handler {
	s := &Server{svc: svc}
	mux := http.NewServeMux()
	static, _ := fs.Sub(videotools.Assets, "web/static")
	fonts, _ := fs.Sub(videotools.Assets, "assets/fonts")
	mux.Handle("GET /static/", longCache(http.StripPrefix("/static/", http.FileServerFS(static))))
	mux.Handle("GET /fonts/", longCache(http.StripPrefix("/fonts/", http.FileServerFS(fonts))))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok\n") })

	mux.HandleFunc("GET /{$}", s.home)
	mux.HandleFunc("GET /panel", s.panelForm)
	mux.HandleFunc("POST /panel", s.panelCreate)
	mux.HandleFunc("GET /join", s.joinForm)
	mux.HandleFunc("POST /join", s.joinCreate)
	mux.HandleFunc("GET /jobs", s.jobsPage)
	mux.HandleFunc("GET /jobs/{id}", s.jobPage)
	mux.HandleFunc("GET /jobs/{id}/status", s.jobStatus)
	mux.HandleFunc("POST /jobs/{id}/render", s.renderUI)
	mux.HandleFunc("POST /jobs/{id}/cancel", s.cancelUI)
	mux.HandleFunc("POST /jobs/{id}/delete", s.deleteUI)
	mux.HandleFunc("GET /jobs/{id}/files/{name}", s.jobFile)
	mux.HandleFunc("GET /status", s.statusPage)
	mux.HandleFunc("GET /source.tar.gz", s.source)
	mux.HandleFunc("GET /login", s.loginForm)
	mux.HandleFunc("POST /login", s.login)

	mux.HandleFunc("GET /api", s.apiIndex)
	mux.HandleFunc("GET /api/status", s.apiStatus)
	mux.HandleFunc("GET /api/files", s.apiFiles)
	mux.HandleFunc("POST /api/files", s.apiUpload)
	mux.HandleFunc("DELETE /api/files/{id}", s.apiDeleteFile)
	mux.HandleFunc("GET /api/jobs", s.apiJobs)
	mux.HandleFunc("GET /api/jobs/{id}", s.apiJob)
	mux.HandleFunc("POST /api/panel", s.apiPanel)
	mux.HandleFunc("POST /api/jobs/{id}/render", s.apiRender)
	mux.HandleFunc("POST /api/join", s.apiJoin)
	mux.HandleFunc("POST /api/jobs/{id}/cancel", s.apiCancel)
	mux.HandleFunc("DELETE /api/jobs/{id}", s.apiDelete)
	if mcpHandler != nil {
		mux.Handle("/mcp", mcpHandler)
	}
	return s.auth(logged(mux))
}

func longCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		h.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(c int)           { w.code = c; w.ResponseWriter.WriteHeader(c) }
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func logged(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, code: 200}
		t := time.Now()
		h.ServeHTTP(sw, r)
		if !strings.HasPrefix(r.URL.Path, "/static/") && !strings.HasSuffix(r.URL.Path, "/status") &&
			r.URL.Path != "/healthz" && !strings.HasPrefix(r.URL.Path, "/fonts/") {
			log.Printf("%s %s %d %s", r.Method, r.URL.Path, sw.code, time.Since(t).Round(time.Millisecond))
		}
	})
}

// ---------------------------------------------------------------- auth

func (s *Server) tokenHash() string {
	h := sha256.Sum256([]byte("video-tools:" + s.svc.Cfg.AuthToken))
	return hex.EncodeToString(h[:])
}

func (s *Server) authorised(r *http.Request) bool {
	tok := s.svc.Cfg.AuthToken
	if tok == "" {
		return true
	}
	if b, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok &&
		subtle.ConstantTimeCompare([]byte(strings.TrimSpace(b)), []byte(tok)) == 1 {
		return true
	}
	if c, err := r.Cookie("vt_auth"); err == nil && subtle.ConstantTimeCompare([]byte(c.Value), []byte(s.tokenHash())) == 1 {
		return true
	}
	return false
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if s.authorised(r) || p == "/healthz" || p == "/login" || strings.HasPrefix(p, "/static/") || strings.HasPrefix(p, "/fonts/") {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(p, "/api") || p == "/mcp" || wantsJSON(r) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="video-tools"`)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing or wrong token: send Authorization: Bearer <AUTH_TOKEN>"})
			return
		}
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	})
}

func (s *Server) loginForm(w http.ResponseWriter, r *http.Request) {
	if s.svc.Cfg.AuthToken == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.render(w, r, views.Login(s.page("Sign in", ""), false))
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	tok := s.svc.Cfg.AuthToken
	if tok == "" || subtle.ConstantTimeCompare([]byte(r.PostFormValue("token")), []byte(tok)) != 1 {
		w.WriteHeader(http.StatusUnauthorized)
		s.render(w, r, views.Login(s.page("Sign in", ""), true))
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "vt_auth", Value: s.tokenHash(), Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, MaxAge: 30 * 24 * 3600, Secure: r.TLS != nil})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// ---------------------------------------------------------------- helpers

func (s *Server) page(title, nav string) views.Page {
	st, _ := s.svc.Models.Status()
	return views.Page{Title: title, Nav: nav, AuthEnabled: s.svc.Cfg.AuthToken != "", ModelsState: st, Version: app.Version}
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, c templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := c.Render(r.Context(), w); err != nil {
		log.Printf("render %s: %v", r.URL.Path, err)
	}
}

func wantsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", " ")
	_ = enc.Encode(v)
}

func errCode(err error) int {
	switch {
	case errors.Is(err, storage.ErrTooLarge):
		return http.StatusRequestEntityTooLarge
	case errors.Is(err, storage.ErrNoSpace):
		return http.StatusInsufficientStorage
	case strings.Contains(err.Error(), "not found"):
		return http.StatusNotFound
	}
	return http.StatusBadRequest
}

// fail answers a form post: JSON for the upload script, a page otherwise.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	code := errCode(err)
	if wantsJSON(r) {
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	_ = views.Layout(s.page("Problem", "")).Render(templ.WithChildren(r.Context(), views.Flash(err.Error(), true)), w)
}

func (s *Server) redirect(w http.ResponseWriter, r *http.Request, to string) {
	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, map[string]string{"redirect": to})
		return
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func first(m map[string][]string, k string) string {
	if v := m[k]; len(v) > 0 {
		return strings.TrimSpace(v[0])
	}
	return ""
}

func (s *Server) baseURL(r *http.Request) string { return BaseURL(s.svc.Cfg.PublicURL, r) }

// BaseURL is the external base URL: PUBLIC_URL if set, else derived from the request.
func BaseURL(public string, r *http.Request) string {
	if public != "" {
		return public
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	return scheme + "://" + host
}

// ---------------------------------------------------------------- pages

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	list := s.svc.Jobs.List()
	if len(list) > 5 {
		list = list[:5]
	}
	s.render(w, r, views.Home(s.page("Home", "home"), list))
}

func (s *Server) panelForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, views.PanelNew(s.page("Panel transcript", "panel"), s.svc.Store.List(),
		storage.Human(s.svc.Cfg.MaxUploadBytes), s.svc.Cfg.MaxTranscriptSecs))
}

func (s *Server) joinForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, views.JoinNew(s.page("Join clips", "join"), s.svc.Store.List(),
		storage.Human(s.svc.Cfg.MaxUploadBytes), s.svc.Cfg.MaxJoinFiles))
}

func (s *Server) panelCreate(w http.ResponseWriter, r *http.Request) {
	files, fields, err := s.svc.Store.SaveUploads(w, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	fileID := first(fields, "file_id")
	for i, f := range files {
		if i == 0 {
			fileID = f.ID
		} else {
			_ = s.svc.Store.DeleteUpload(f.ID)
		}
	}
	cleanup := func() {
		if len(files) > 0 {
			_ = s.svc.Store.DeleteUpload(files[0].ID)
		}
	}
	if fileID == "" {
		s.fail(w, r, errors.New("choose a clip first"))
		return
	}
	var p panel.Params
	if first(fields, "frames") == "times" {
		if p.Times, err = panel.ParseTimes(first(fields, "times")); err != nil || len(p.Times) == 0 {
			cleanup()
			if err == nil {
				err = errors.New("enter at least one time, for example 1.0, 2.5, 7.2")
			}
			s.fail(w, r, err)
			return
		}
	}
	if v := first(fields, "scene_threshold"); v != "" {
		p.SceneThreshold, _ = strconv.ParseFloat(v, 64)
	}
	j, err := s.svc.StartPanel(fileID, p)
	if err != nil {
		cleanup()
		s.fail(w, r, err)
		return
	}
	s.redirect(w, r, "/jobs/"+j.ID)
}

func (s *Server) joinCreate(w http.ResponseWriter, r *http.Request) {
	files, fields, err := s.svc.Store.SaveUploads(w, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	used := map[string]bool{}
	var ids []string
	if order := first(fields, "order"); order != "" {
		for _, tok := range strings.Split(order, ",") {
			switch {
			case strings.HasPrefix(tok, "new:"):
				n, err := strconv.Atoi(strings.TrimPrefix(tok, "new:"))
				if err == nil && n >= 0 && n < len(files) {
					ids = append(ids, files[n].ID)
					used[files[n].ID] = true
				}
			case strings.HasPrefix(tok, "id:"):
				ids = append(ids, strings.TrimPrefix(tok, "id:"))
			}
		}
	} else { // no script: uploads in the order the browser sent them
		for _, f := range files {
			ids = append(ids, f.ID)
			used[f.ID] = true
		}
	}
	cleanup := func(all bool) {
		for _, f := range files {
			if all || !used[f.ID] {
				_ = s.svc.Store.DeleteUpload(f.ID)
			}
		}
	}
	cleanup(false)
	secs, _ := strconv.ParseFloat(first(fields, "seconds"), 64)
	req := app.JoinRequest{FileIDs: ids, Transition: first(fields, "transition"), Seconds: secs,
		Mode: first(fields, "mode"), FadeEdges: first(fields, "fade_edges") == "true", Name: first(fields, "name")}
	if req.Transition == "none" {
		req.Seconds = 0
	}
	j, err := s.svc.StartJoin(req)
	if err != nil {
		cleanup(true)
		s.fail(w, r, err)
		return
	}
	s.redirect(w, r, "/jobs/"+j.ID)
}

func (s *Server) jobsPage(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, views.JobsPage(s.page("Jobs", "jobs"), s.svc.Jobs.List()))
}

func (s *Server) jobPage(w http.ResponseWriter, r *http.Request) {
	j, err := s.svc.Jobs.Get(r.PathValue("id"))
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		s.render(w, r, views.JobsPage(s.page("Jobs", "jobs"), s.svc.Jobs.List()))
		return
	}
	var pv *app.PanelView
	if j.Kind == "panel" && (j.Status == jobs.Review || j.Status == jobs.Done) {
		if v, err := s.svc.PanelState(j.ID); err == nil {
			pv = &v
		}
	}
	s.render(w, r, views.JobPage(s.page(j.Title, "jobs"), j, pv))
}

func (s *Server) jobStatus(w http.ResponseWriter, r *http.Request) {
	j, err := s.svc.Jobs.Get(r.PathValue("id"))
	if err != nil || !j.Status.Active() {
		w.Header().Set("HX-Refresh", "true")
		return
	}
	s.render(w, r, views.JobStatus(j))
}

func (s *Server) renderUI(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		s.render(w, r, views.Flash(err.Error(), true))
		return
	}
	sp, tx, fr := r.Form["speaker"], r.Form["text"], r.Form["frame"]
	if len(sp) != len(tx) || len(tx) != len(fr) {
		s.render(w, r, views.Flash("the form was incomplete; reload the page and try again", true))
		return
	}
	var rv panel.Review
	for i := range sp {
		f, err := strconv.Atoi(fr[i])
		if err != nil {
			f = -1
		}
		rv.Lines = append(rv.Lines, panel.ReviewLine{Speaker: sp[i], Text: tx[i], Frame: f})
	}
	j, err := s.svc.RenderPanel(r.Context(), id, rv)
	if err != nil {
		s.render(w, r, views.Flash(err.Error(), true))
		return
	}
	pv, err := s.svc.PanelState(id)
	if err != nil {
		s.render(w, r, views.Flash(err.Error(), true))
		return
	}
	s.render(w, r, views.PanelResult(j, pv))
}

func (s *Server) cancelUI(w http.ResponseWriter, r *http.Request) {
	_ = s.svc.Jobs.Cancel(r.PathValue("id"))
	w.Header().Set("HX-Refresh", "true")
}

func (s *Server) deleteUI(w http.ResponseWriter, r *http.Request) {
	_ = s.svc.Jobs.Delete(r.PathValue("id"))
	w.Header().Set("HX-Redirect", "/jobs")
}

func (s *Server) jobFile(w http.ResponseWriter, r *http.Request) {
	p, err := s.svc.OutputPath(r.PathValue("id"), r.PathValue("name"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if ct := mime.TypeByExtension(filepath.Ext(p)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	if strings.HasSuffix(p, ".md") {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	}
	w.Header().Set("Cache-Control", "private, max-age=60")
	http.ServeFile(w, r, p) // handles Range requests, so the video player can seek
}

func (s *Server) statusPage(w http.ResponseWriter, r *http.Request) {
	c := s.svc.Cfg
	limits := []views.Limit{
		{Name: "Largest upload (per request)", Value: storage.Human(c.MaxUploadBytes), Env: "MAX_UPLOAD_MB"},
		{Name: "Free space always kept", Value: storage.Human(c.MinFreeBytes), Env: "MIN_FREE_MB"},
		{Name: "Jobs and uploads deleted after", Value: fmt.Sprintf("%g hours", c.JobTTL.Hours()), Env: "JOB_TTL_HOURS"},
		{Name: "Jobs running at once", Value: strconv.Itoa(c.Workers), Env: "WORKERS"},
		{Name: "ffmpeg threads per job", Value: strconv.Itoa(c.FFmpegThreads), Env: "FFMPEG_THREADS"},
		{Name: "Speech model threads", Value: strconv.Itoa(c.ASRThreads), Env: "ASR_THREADS"},
		{Name: "Longest clip for a panel transcript", Value: fmt.Sprintf("%d s", c.MaxTranscriptSecs), Env: "MAX_TRANSCRIPT_SECONDS"},
		{Name: "Most clips in one join", Value: strconv.Itoa(c.MaxJoinFiles), Env: "MAX_JOIN_FILES"},
		{Name: "Re-encoded joins scaled down to", Value: fmt.Sprintf("%d px high", c.JoinMaxHeight), Env: "JOIN_MAX_HEIGHT"},
		{Name: "Keep uploaded clips after a join", Value: strconv.FormatBool(c.KeepJoinInputs), Env: "KEEP_JOIN_INPUTS"},
		{Name: "Inbox folder (read-only inputs)", Value: c.InboxDir, Env: "INBOX_DIR"},
	}
	s.render(w, r, views.StatusPage(s.page("Status", "status"), s.svc.Status(), c, limits, s.baseURL(r)+"/mcp"))
}

// source streams this project's source tree (shipped in the image) as a .tar.gz.
func (s *Server) source(w http.ResponseWriter, r *http.Request) {
	root := s.svc.Cfg.SourceDir
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		http.Error(w, "source isn't bundled in this build (SOURCE_DIR "+root+")", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="video-tools-src.tar.gz"`)
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "data" || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		hdr, _ := tar.FileInfoHeader(info, "")
		hdr.Name = "video-tools/" + filepath.ToSlash(rel)
		if tw.WriteHeader(hdr) != nil {
			return io.EOF
		}
		f, err := os.Open(p)
		if err == nil {
			_, _ = io.Copy(tw, f)
			f.Close()
		}
		return nil
	})
	_ = tw.Close()
	_ = gz.Close()
}

// ---------------------------------------------------------------- JSON API

func (s *Server) apiIndex(w http.ResponseWriter, r *http.Request) {
	b := s.baseURL(r)
	writeJSON(w, 200, map[string]any{
		"service": "video-tools " + app.Version,
		"auth":    "if AUTH_TOKEN is set, send Authorization: Bearer <token>",
		"mcp":     b + "/mcp (streamable HTTP; tools describe the workflow)",
		"endpoints": []string{
			"GET  /api/status",
			"POST /api/files            multipart, field 'file' (repeatable) -> [{id,name,size}]",
			"GET  /api/files            uploaded + inbox clips",
			"DELETE /api/files/{id}",
			"POST /api/panel            {file_id, times?:[sec], scene_threshold?, lines?:[{text,start,end}]} -> job (lines skip speech recognition)",
			"GET  /api/jobs/{id}        job; panel jobs include lines, frames and image URLs",
			"POST /api/jobs/{id}/render {lines:[{speaker, text?, panel?}]} panel is 1-based, 0 = leave off",
			"POST /api/join             {file_ids:[], transition:none|fade|crossfade, seconds?, mode?:auto|copy|reencode, fade_edges?, name?} -> job",
			"POST /api/jobs/{id}/cancel",
			"DELETE /api/jobs/{id}",
			"GET  /jobs/{id}/files/{name} download an output",
		},
		"example": "curl -F file=@clip.mov " + b + "/api/files",
	})
}

func (s *Server) apiStatus(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, s.svc.Status()) }

func (s *Server) apiFiles(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"files": s.svc.Store.List()})
}

func (s *Server) apiUpload(w http.ResponseWriter, r *http.Request) {
	files, _, err := s.svc.Store.SaveUploads(w, r)
	if err != nil {
		writeJSON(w, errCode(err), map[string]string{"error": err.Error()})
		return
	}
	if len(files) == 0 {
		writeJSON(w, 400, map[string]string{"error": "no file in the upload (use multipart field 'file')"})
		return
	}
	writeJSON(w, 201, map[string]any{"files": files})
}

func (s *Server) apiDeleteFile(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.DeleteFile(r.PathValue("id")); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) apiJobs(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"jobs": s.svc.Jobs.List()})
}

// JobDetail is a job plus, for panel jobs, what an agent needs to name speakers.
type JobDetail struct {
	jobs.Job
	Files map[string]string `json:"files,omitempty"` // output name -> URL
	Panel *PanelDetail      `json:"panel,omitempty"`
}

type PanelDetail struct {
	Duration  float64            `json:"duration"`
	SceneCuts []float64          `json:"scene_cuts"`
	Frames    []FrameDetail      `json:"frames"`
	Lines     []LineDetail       `json:"lines"`
	Rendered  bool               `json:"rendered"`
	Review    []panel.ReviewLine `json:"current_choices"`
}

type FrameDetail struct {
	Panel int     `json:"panel"`
	Time  float64 `json:"time"`
	Image string  `json:"image_url"`
}

type LineDetail struct {
	Line         int          `json:"line"`
	Start        float64      `json:"start"`
	End          float64      `json:"end"`
	Text         string       `json:"text"`
	Panel        int          `json:"panel"`
	Pitch        *panel.Pitch `json:"pitch_hz,omitempty"`
	ContactSheet string       `json:"contact_sheet_url"`
}

// Detail builds the API view of a job (also used by MCP).
func Detail(svc *app.Service, base, id string) (JobDetail, error) {
	j, err := svc.Jobs.Get(id)
	if err != nil {
		return JobDetail{}, err
	}
	d := JobDetail{Job: j, Files: map[string]string{}}
	u := func(n string) string { return base + "/jobs/" + j.ID + "/files/" + n }
	for _, o := range j.Outputs {
		d.Files[o.Name] = u(o.Name)
	}
	if j.Kind == "panel" && (j.Status == jobs.Review || j.Status == jobs.Done) {
		if pv, err := svc.PanelState(id); err == nil {
			pd := &PanelDetail{Duration: pv.Analysis.Duration, SceneCuts: pv.Analysis.SceneCuts, Rendered: pv.Rendered,
				Review: pv.Review.Lines}
			for i, f := range pv.Analysis.Frames {
				pd.Frames = append(pd.Frames, FrameDetail{Panel: i + 1, Time: f.T, Image: u(f.Image)})
			}
			for i, l := range pv.Analysis.Lines {
				pd.Lines = append(pd.Lines, LineDetail{Line: i + 1, Start: l.Start, End: l.End, Text: l.Text,
					Panel: l.Frame + 1, Pitch: l.Pitch, ContactSheet: u(l.ContactSheet)})
			}
			d.Panel = pd
		}
	}
	return d, nil
}

func (s *Server) apiJob(w http.ResponseWriter, r *http.Request) {
	d, err := Detail(s.svc, s.baseURL(r), r.PathValue("id"))
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, d)
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("bad JSON body: %w", err)
	}
	return nil
}

func (s *Server) apiPanel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FileID         string            `json:"file_id"`
		Times          []float64         `json:"times"`
		SceneThreshold float64           `json:"scene_threshold"`
		Lines          []panel.GivenLine `json:"lines"`
	}
	if err := decode(r, &req); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	j, err := s.svc.StartPanel(req.FileID, panel.Params{Times: req.Times, SceneThreshold: req.SceneThreshold, Lines: req.Lines})
	if err != nil {
		writeJSON(w, errCode(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 202, j)
}

// RenderRequest is the agent-facing render body: one entry per transcript line, in order.
type RenderRequest struct {
	Lines []struct {
		Speaker string `json:"speaker"`
		Text    string `json:"text,omitempty"`
		Panel   *int   `json:"panel,omitempty"` // 1-based; 0 = leave off; omitted = keep current
	} `json:"lines"`
}

// ToReview converts a RenderRequest into a review, keeping current choices where omitted.
func ToReview(svc *app.Service, id string, req RenderRequest) (panel.Review, error) {
	pv, err := svc.PanelState(id)
	if err != nil {
		return panel.Review{}, err
	}
	if len(req.Lines) != len(pv.Analysis.Lines) {
		return panel.Review{}, fmt.Errorf("send exactly %d lines (one per transcript line, in order); got %d",
			len(pv.Analysis.Lines), len(req.Lines))
	}
	var rv panel.Review
	for i, l := range req.Lines {
		cur := pv.Review.Lines[i]
		rl := panel.ReviewLine{Speaker: l.Speaker, Text: l.Text, Frame: cur.Frame}
		if rl.Text == "" {
			rl.Text = cur.Text
		}
		if l.Panel != nil {
			rl.Frame = *l.Panel - 1
		}
		rv.Lines = append(rv.Lines, rl)
	}
	return rv, nil
}

func (s *Server) apiRender(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req RenderRequest
	if err := decode(r, &req); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	rv, err := ToReview(s.svc, id, req)
	if err != nil {
		writeJSON(w, errCode(err), map[string]string{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	if _, err := s.svc.RenderPanel(ctx, id, rv); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	s.apiJob(w, r)
}

func (s *Server) apiJoin(w http.ResponseWriter, r *http.Request) {
	var req app.JoinRequest
	if err := decode(r, &req); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	j, err := s.svc.StartJoin(req)
	if err != nil {
		writeJSON(w, errCode(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 202, j)
}

func (s *Server) apiCancel(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.Jobs.Cancel(r.PathValue("id")); err != nil {
		writeJSON(w, 404, map[string]string{"error": err.Error()})
		return
	}
	s.apiJob(w, r)
}

func (s *Server) apiDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.Jobs.Delete(r.PathValue("id")); err != nil {
		writeJSON(w, 404, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
