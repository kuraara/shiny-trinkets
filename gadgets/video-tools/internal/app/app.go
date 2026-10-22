// Package app is the service layer shared by the web UI, the JSON API and the MCP endpoint.
package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/kuraara/clowd-docker-stuff/video-tools/internal/config"
	"github.com/kuraara/clowd-docker-stuff/video-tools/internal/jobs"
	"github.com/kuraara/clowd-docker-stuff/video-tools/internal/media"
	"github.com/kuraara/clowd-docker-stuff/video-tools/internal/models"
	"github.com/kuraara/clowd-docker-stuff/video-tools/internal/panel"
	"github.com/kuraara/clowd-docker-stuff/video-tools/internal/storage"
)

// Version is set at build time (-ldflags "-X .../app.Version=...").
var Version = "dev"

type Service struct {
	Cfg    config.Config
	Store  *storage.Store
	Jobs   *jobs.Manager
	Models *models.Models
	Worker panel.Worker

	renderMu sync.Map // per-job render lock
}

func New(cfg config.Config) (*Service, error) {
	st, err := storage.New(cfg.DataDir, cfg.InboxDir, cfg.MinFreeBytes, cfg.MaxUploadBytes)
	if err != nil {
		return nil, fmt.Errorf("data dir %s: %w", cfg.DataDir, err)
	}
	if err := os.MkdirAll(cfg.ModelsDir, 0o755); err != nil {
		return nil, err
	}
	s := &Service{
		Cfg:    cfg,
		Store:  st,
		Jobs:   jobs.NewManager(st, cfg.JobTTL),
		Models: models.New(cfg.ModelsDir, cfg.SetupModel),
		Worker: panel.Worker{Python: cfg.Python, Dir: cfg.WorkerDir, ModelsDir: cfg.ModelsDir, FontsDir: cfg.FontsDir,
			ASRThreads: cfg.ASRThreads, FFThreads: cfg.FFmpegThreads, MaxSecs: cfg.MaxTranscriptSecs},
	}
	s.Jobs.Register("panel", s.runPanel)
	s.Jobs.Register("join", s.runJoin)
	return s, nil
}

func (s *Service) Start(ctx context.Context) {
	s.Models.EnsureAsync(ctx)
	s.Jobs.Start(ctx, s.Cfg.Workers)
}

// ---------------------------------------------------------------- panel transcript

// StartPanel queues analysis of one clip. Empty times = auto (one panel per spoken line).
func (s *Service) StartPanel(fileID string, p panel.Params) (jobs.Job, error) {
	f, err := s.Store.Resolve(fileID)
	if err != nil {
		return jobs.Job{}, err
	}
	if p.SceneThreshold != 0 && (p.SceneThreshold < 0.05 || p.SceneThreshold > 0.95) {
		return jobs.Job{}, errors.New("scene_threshold must be between 0.05 and 0.95 (default 0.3)")
	}
	for _, t := range p.Times {
		if t < 0 {
			return jobs.Job{}, errors.New("times can't be negative")
		}
	}
	if err := s.Store.Reserve(64 << 20); err != nil {
		return jobs.Job{}, err
	}
	return s.Jobs.Submit("panel", f.Name, []storage.File{f}, p)
}

func (s *Service) runPanel(ctx context.Context, h *jobs.Handle) error {
	if !s.Models.Ready() {
		s.Models.EnsureAsync(context.Background())
		if st, d := s.Models.Status(); st == "failed" {
			return fmt.Errorf("speech models unavailable: %s", d)
		}
		h.Progress(0, "waiting for the speech models to finish downloading")
		return jobs.ErrRequeue
	}
	var p panel.Params
	if err := h.Params(&p); err != nil {
		return err
	}
	in := h.Job().Inputs[0]
	f, err := s.Store.Resolve(in.ID)
	if err != nil {
		return err
	}
	a, err := s.Worker.Analyse(ctx, f.Path, h.OutDir(), h.WorkDir(), p, h.Progress, h.Log)
	if err != nil {
		return err
	}
	for _, w := range a.Warnings {
		h.Note(w)
	}
	h.FinishAs(jobs.Review, "ready — add speaker names, then render")
	return nil
}

// PanelView is everything the review screen and agents need.
type PanelView struct {
	Analysis panel.Analysis `json:"analysis"`
	Review   panel.Review   `json:"review"`
	Rendered bool           `json:"rendered"`
}

func (s *Service) PanelState(id string) (PanelView, error) {
	j, err := s.Jobs.Get(id)
	if err != nil {
		return PanelView{}, err
	}
	if j.Kind != "panel" {
		return PanelView{}, errors.New("not a panel transcript job")
	}
	dir := s.Jobs.OutDir(id)
	a, err := panel.LoadAnalysis(dir)
	if err != nil {
		return PanelView{}, err
	}
	r, _ := panel.LoadReview(dir)
	_, statErr := os.Stat(filepath.Join(dir, "panel_transcript.png"))
	return PanelView{Analysis: a, Review: r, Rendered: statErr == nil}, nil
}

// RenderPanel saves speaker names / edits and draws the strip. Safe to call repeatedly.
func (s *Service) RenderPanel(ctx context.Context, id string, upd panel.Review) (jobs.Job, error) {
	j, err := s.Jobs.Get(id)
	if err != nil {
		return j, err
	}
	if j.Kind != "panel" {
		return j, errors.New("not a panel transcript job")
	}
	if j.Status != jobs.Review && j.Status != jobs.Done {
		return j, fmt.Errorf("job is %s; it can be rendered once analysis has finished", j.Status)
	}
	mu, _ := s.renderMu.LoadOrStore(id, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()
	dir := s.Jobs.OutDir(id)
	a, err := panel.LoadAnalysis(dir)
	if err != nil {
		return j, err
	}
	prev, _ := panel.LoadReview(dir)
	r, err := panel.Merge(a, prev, upd)
	if err != nil {
		return j, err
	}
	if err := panel.SaveReview(dir, r); err != nil {
		return j, err
	}
	names, err := s.Worker.Render(ctx, dir, a, r)
	if err != nil {
		return j, fmt.Errorf("render failed: %w", err)
	}
	labels := map[string][2]string{
		"panel_transcript.png": {"Panel transcript (PNG)", "image"},
		"transcript.md":        {"Transcript (Markdown)", "text"},
		"transcript.json":      {"Transcript (JSON)", "json"},
	}
	_ = s.Jobs.Update(id, func(j *jobs.Job) {
		j.Status, j.Message, j.Progress = jobs.Done, "rendered", 100
		j.Outputs = nil
		for _, n := range names {
			var size int64
			if st, err := os.Stat(filepath.Join(dir, n)); err == nil {
				size = st.Size()
			}
			j.Outputs = append(j.Outputs, jobs.Output{Name: n, Label: labels[n][0], Kind: labels[n][1], Size: size})
		}
	})
	return s.Jobs.Get(id)
}

// ---------------------------------------------------------------- join

type JoinRequest struct {
	FileIDs    []string `json:"file_ids"`
	Transition string   `json:"transition"` // none | fade | crossfade
	Seconds    float64  `json:"seconds"`    // transition length (default 0.5)
	Mode       string   `json:"mode"`       // auto | copy | reencode
	FadeEdges  bool     `json:"fade_edges"`
	Name       string   `json:"name"` // output file name (no extension)
}

type joinParams struct {
	Options media.JoinOptions `json:"options"`
	Name    string            `json:"name"`
}

var nameRe = regexp.MustCompile(`[^A-Za-z0-9._ -]+`)

func (s *Service) StartJoin(req JoinRequest) (jobs.Job, error) {
	if len(req.FileIDs) < 2 {
		return jobs.Job{}, errors.New("pick at least two clips")
	}
	if len(req.FileIDs) > s.Cfg.MaxJoinFiles {
		return jobs.Job{}, fmt.Errorf("at most %d clips per join (MAX_JOIN_FILES)", s.Cfg.MaxJoinFiles)
	}
	var files []storage.File
	for _, id := range req.FileIDs {
		f, err := s.Store.Resolve(strings.TrimSpace(id))
		if err != nil {
			return jobs.Job{}, err
		}
		files = append(files, f)
	}
	if req.Transition == "" {
		req.Transition = "none"
	}
	if req.Transition != "none" && req.Transition != "fade" && req.Transition != "crossfade" {
		return jobs.Job{}, errors.New("transition must be none, fade or crossfade")
	}
	if req.Mode == "" {
		req.Mode = "auto"
	}
	if req.Mode != "auto" && req.Mode != "copy" && req.Mode != "reencode" {
		return jobs.Job{}, errors.New("mode must be auto, copy or reencode")
	}
	if req.Seconds < 0 || req.Seconds > 5 {
		return jobs.Job{}, errors.New("seconds must be between 0 and 5")
	}
	if req.Transition != "none" && req.Seconds == 0 {
		req.Seconds = 0.5
	}
	name := strings.TrimSpace(nameRe.ReplaceAllString(strings.TrimSuffix(req.Name, filepath.Ext(req.Name)), "_"))
	if name == "" || name == "." || name == ".." {
		name = "joined"
	}
	title := fmt.Sprintf("%d clips joined", len(files))
	switch req.Transition {
	case "fade":
		title += " with fades"
	case "crossfade":
		title += " with crossfades"
	}
	return s.Jobs.Submit("join", title, files, joinParams{
		Options: media.JoinOptions{Transition: req.Transition, Seconds: req.Seconds, Mode: req.Mode, FadeEdges: req.FadeEdges},
		Name:    name,
	})
}

func (s *Service) runJoin(ctx context.Context, h *jobs.Handle) error {
	var p joinParams
	if err := h.Params(&p); err != nil {
		return err
	}
	p.Options.MaxHeight = s.Cfg.JoinMaxHeight
	var infos []media.Info
	inputs := h.Job().Inputs
	for i, in := range inputs {
		h.Progress(0, fmt.Sprintf("checking clip %d of %d", i+1, len(inputs)))
		f, err := s.Store.Resolve(in.ID)
		if err != nil {
			return err
		}
		inf, err := media.Probe(ctx, f.Path)
		if err != nil {
			return fmt.Errorf("clip %d (%s): %w", i+1, f.Name, err)
		}
		inf.Name = f.Name
		infos = append(infos, inf)
	}
	plan, err := media.PlanJoin(infos, p.Options)
	if err != nil {
		return err
	}
	if plan.Copy {
		h.Note("Joined without re-encoding (stream copy): original quality, no transcoding.")
	} else {
		h.Note(fmt.Sprintf("Re-encoded to H.264/AAC %dx%d at %s fps.", plan.Target.W, plan.Target.H, plan.Target.FPS))
		for _, w := range plan.Why {
			h.Note("Why re-encode: " + w)
		}
		if p.Options.Transition != "none" && plan.Seconds != p.Options.Seconds {
			h.Note(fmt.Sprintf("Transition shortened to %.2fs to fit the shortest clip.", plan.Seconds))
		}
	}
	if err := s.Store.Reserve(plan.EstBytes); err != nil {
		return err
	}
	out := filepath.Join(h.OutDir(), p.Name+plan.Ext)
	ff := media.FF{Threads: s.Cfg.FFmpegThreads, Log: h.Log}
	if err := media.Join(ctx, infos, p.Options, plan, h.WorkDir(), out, ff, h.Progress); err != nil {
		_ = os.Remove(out)
		return err
	}
	h.AddOutput(filepath.Base(out), "Joined video", "video")
	if !s.Cfg.KeepJoinInputs {
		n := 0
		for _, in := range inputs {
			if strings.HasPrefix(in.ID, "f_") && !h.InputInUseElsewhere(in.ID) && s.Store.DeleteUpload(in.ID) == nil {
				n++
			}
		}
		if n > 0 {
			h.Note(fmt.Sprintf("Deleted %d uploaded source clip(s) to free space (KEEP_JOIN_INPUTS=false).", n))
		}
	}
	return nil
}

// ---------------------------------------------------------------- status

type Status struct {
	Version      string `json:"version"`
	DataFree     int64  `json:"data_free_bytes"`
	DataTotal    int64  `json:"data_total_bytes"`
	JobsBytes    int64  `json:"jobs_bytes"`
	FilesBytes   int64  `json:"uploads_bytes"`
	ModelsState  string `json:"models_state"`
	ModelsDetail string `json:"models_detail,omitempty"`
	Queued       int    `json:"jobs_queued"`
	Running      int    `json:"jobs_running"`
	Total        int    `json:"jobs_total"`
}

func (s *Service) Status() Status {
	st := Status{Version: Version, DataFree: storage.Free(s.Cfg.DataDir), DataTotal: storage.Total(s.Cfg.DataDir),
		JobsBytes: storage.DirSize(s.Store.JobsDir), FilesBytes: storage.DirSize(s.Store.FilesDir)}
	st.ModelsState, st.ModelsDetail = s.Models.Status()
	for _, j := range s.Jobs.List() {
		st.Total++
		switch j.Status {
		case jobs.Queued:
			st.Queued++
		case jobs.Running:
			st.Running++
		}
	}
	return st
}

// DeleteFile removes an upload unless an active job needs it.
func (s *Service) DeleteFile(id string) error {
	if s.Jobs.InputInUse(id) {
		return errors.New("a running job is still using this file")
	}
	return s.Store.DeleteUpload(id)
}

// OutputPath returns the path of a job's output file, refusing anything outside out/.
func (s *Service) OutputPath(jobID, name string) (string, error) {
	if _, err := s.Jobs.Get(jobID); err != nil {
		return "", err
	}
	if name == "" || strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") {
		return "", errors.New("bad file name")
	}
	p := filepath.Join(s.Jobs.OutDir(jobID), name)
	if _, err := os.Stat(p); err != nil {
		return "", errors.New("file not found")
	}
	return p, nil
}
