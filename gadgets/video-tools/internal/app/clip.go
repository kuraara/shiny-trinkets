package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kuraara/clowd-docker-stuff/video-tools/internal/jobs"
	"github.com/kuraara/clowd-docker-stuff/video-tools/internal/media"
	"github.com/kuraara/clowd-docker-stuff/video-tools/internal/panel"
	"github.com/kuraara/clowd-docker-stuff/video-tools/internal/storage"
)

// ---------------------------------------------------------------- clip

const maxClipSeconds = 30 * 60

type ClipRequest struct {
	FileID    string            `json:"file_id"`
	Start     float64           `json:"start"` // seconds
	End       float64           `json:"end"`
	Mode      string            `json:"mode"`       // accurate (default) | fast
	MaxHeight int               `json:"max_height"` // accurate: default 1080, 0 = default, -1 = keep
	Subtitles []panel.GivenLine `json:"subtitles"`  // burned in (accurate only); times from the start of the source
	Name      string            `json:"name"`       // output file name (no extension)
	// DeleteInput removes the uploaded source once the clip is made (used when the source is a
	// throwaway excerpt, e.g. from Quote Finder).
	DeleteInput bool `json:"delete_input"`
}

type clipParams struct {
	ClipRequest
}

func (s *Service) StartClip(req ClipRequest) (jobs.Job, error) {
	f, err := s.Store.Resolve(strings.TrimSpace(req.FileID))
	if err != nil {
		return jobs.Job{}, err
	}
	if req.Mode == "" {
		req.Mode = "accurate"
	}
	if req.Mode != "accurate" && req.Mode != "fast" {
		return jobs.Job{}, errors.New("mode must be accurate or fast")
	}
	if req.Start < 0 || req.End <= req.Start {
		return jobs.Job{}, errors.New("start must be ≥ 0 and before end")
	}
	if req.End-req.Start > maxClipSeconds {
		return jobs.Job{}, fmt.Errorf("a clip can be at most %d minutes", maxClipSeconds/60)
	}
	if len(req.Subtitles) > 0 && req.Mode == "fast" {
		return jobs.Job{}, errors.New("burned-in subtitles need the accurate mode (it re-encodes)")
	}
	if len(req.Subtitles) > 500 {
		return jobs.Job{}, errors.New("at most 500 subtitle lines")
	}
	if req.MaxHeight == 0 {
		req.MaxHeight = 1080
	}
	name := strings.TrimSpace(nameRe.ReplaceAllString(strings.TrimSuffix(req.Name, filepath.Ext(req.Name)), "_"))
	if name == "" || name == "." || name == ".." {
		name = strings.TrimSpace(nameRe.ReplaceAllString(strings.TrimSuffix(f.Name, filepath.Ext(f.Name)), "_")) + " clip"
	}
	req.Name = name
	title := fmt.Sprintf("%s (%s–%s)", name, clock(req.Start), clock(req.End))
	return s.Jobs.Submit("clip", title, []storage.File{f}, clipParams{req})
}

func clock(sec float64) string {
	t := int(sec)
	if t >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", t/3600, t/60%60, t%60)
	}
	return fmt.Sprintf("%d:%02d", t/60, t%60)
}

func (s *Service) runClip(ctx context.Context, h *jobs.Handle) error {
	var p clipParams
	if err := h.Params(&p); err != nil {
		return err
	}
	in := h.Job().Inputs[0]
	f, err := s.Store.Resolve(in.ID)
	if err != nil {
		return err
	}
	h.Progress(0, "checking the source")
	inf, err := media.Probe(ctx, f.Path)
	if err != nil {
		return err
	}
	if p.Start >= inf.Duration {
		return fmt.Errorf("the source is only %s long", clock(inf.Duration))
	}
	o := media.ClipOptions{Start: p.Start, End: min(p.End, inf.Duration), Mode: p.Mode, FontsDir: s.Cfg.FontsDir}
	if p.MaxHeight > 0 {
		o.MaxHeight = min(p.MaxHeight, s.Cfg.JoinMaxHeight)
	}
	if len(p.Subtitles) > 0 {
		var cues []media.Cue
		for _, l := range p.Subtitles {
			cues = append(cues, media.Cue{Text: l.Text, Start: l.Start - o.Start, End: min(l.End, o.End) - o.Start})
		}
		o.Subtitles = filepath.Join(h.WorkDir(), "subs.srt")
		if err := os.MkdirAll(h.WorkDir(), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(o.Subtitles, []byte(media.SRT(cues)), 0o644); err != nil {
			return err
		}
	}
	d := o.End - o.Start
	est := int64(float64(inf.Size) / inf.Duration * d * 1.5)
	if o.Mode == "accurate" {
		est = int64(d * 1.5e6) // ~12 Mbit/s ceiling at 1080p CRF 20
	}
	if err := s.Store.Reserve(est); err != nil {
		return err
	}
	out := filepath.Join(h.OutDir(), p.Name+media.ClipExt(inf, o))
	ff := media.FF{Threads: s.Cfg.FFmpegThreads, Log: h.Log}
	h.Progress(1, "cutting")
	err = ff.Run(ctx, media.ClipArgs(inf, o, s.Cfg.FFmpegThreads, out), func(sec float64) {
		h.Progress(min(99, int(sec/d*100)), "cutting")
	})
	if err != nil {
		_ = os.Remove(out)
		return err
	}
	if o.Mode == "fast" {
		h.Note("Copied without re-encoding: original quality. It starts on the nearest keyframe at or before the start time.")
	} else {
		h.Note("Re-encoded to H.264/AAC MP4, frame-accurate.")
		if media.IsHDR(inf) {
			h.Note("The source is HDR; colours were converted to standard range so it looks right on any screen.")
		}
		if o.Subtitles != "" {
			h.Note(fmt.Sprintf("Burned in %d subtitle lines.", len(p.Subtitles)))
		}
	}
	h.AddOutput(filepath.Base(out), "Clip", "video")
	if p.DeleteInput && strings.HasPrefix(in.ID, "f_") && !h.InputInUseElsewhere(in.ID) {
		_ = s.Store.DeleteUpload(in.ID)
	}
	return nil
}
