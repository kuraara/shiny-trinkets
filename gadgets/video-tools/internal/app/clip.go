package app

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/kuraara/shiny-trinkets/gadgets/video-tools/internal/jobs"
	"github.com/kuraara/shiny-trinkets/gadgets/video-tools/internal/media"
	"github.com/kuraara/shiny-trinkets/gadgets/video-tools/internal/panel"
	"github.com/kuraara/shiny-trinkets/gadgets/video-tools/internal/storage"
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
	// Align, when given, moves the cut to the speech: the lines (e.g. subtitles, which are only
	// roughly right) are matched against the words actually spoken, and the clip runs from just
	// before the first chosen line is said to just after the last one ends, without running into
	// the neighbouring lines. Start/End above are the fallback if that can't be done. Burned-in
	// subtitles are retimed to the speech too.
	Align *ClipAlign `json:"align,omitempty"`
}

// ClipAlign: the lines around the cut (times in seconds into the source) and which of them
// (First..Last, 0-based) the clip should hold; Lead/Tail are the room left around the speech.
type ClipAlign struct {
	Lines []panel.GivenLine `json:"lines"`
	First int               `json:"first"`
	Last  int               `json:"last"`
	Lead  float64           `json:"lead"` // default 0.5 s
	Tail  float64           `json:"tail"` // default 0.7 s
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
	if al := req.Align; al != nil {
		if err := panel.CheckLinesMax(al.Lines, 400); err != nil {
			return jobs.Job{}, fmt.Errorf("align: %w", err)
		}
		if len(al.Lines) == 0 || al.First < 0 || al.Last < al.First || al.Last >= len(al.Lines) {
			return jobs.Job{}, errors.New("align: first and last must pick lines from the list, first before last")
		}
		if al.Lead <= 0 {
			al.Lead = 0.5
		}
		if al.Tail <= 0 {
			al.Tail = 0.7
		}
		al.Lead, al.Tail = min(al.Lead, 5), min(al.Tail, 5)
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
	if al := p.Align; al != nil {
		switch {
		case !s.Models.Ready():
			h.Note("Cut on the subtitle times: the speech models aren’t downloaded yet, so the cut wasn’t lined up with the speech.")
		default:
			h.Progress(2, "lining the cut up with the speech")
			res, err := s.Worker.Align(ctx, f.Path, h.WorkDir(), al.Lines, al.First, al.Last, al.Lead, al.Tail,
				func(pct int, msg string) { h.Progress(2+pct*28/100, msg) }, h.Log)
			switch {
			case err != nil:
				if ctx.Err() != nil {
					return ctx.Err()
				}
				h.Note("Cut on the subtitle times: lining up with the speech failed (" + err.Error() + ").")
			case res.Matched == 0:
				h.Note("Cut on the subtitle times: none of the subtitle words were recognised in the speech.")
			default:
				o.Start, o.End = res.Cut.Start, min(res.Cut.End, inf.Duration)
				msg := fmt.Sprintf("Cut lined up with the speech (%d of the subtitle words heard).", res.Matched)
				if math.Abs(res.Offset) >= 0.5 {
					early := "early"
					if res.Offset < 0 {
						early = "late"
					}
					msg += fmt.Sprintf(" The subtitles were about %.1f s %s.", math.Abs(res.Offset), early)
				}
				h.Note(msg)
				if len(p.Subtitles) > 0 { // burn in the chosen lines where they're really spoken
					p.Subtitles = p.Subtitles[:0]
					for _, l := range res.Lines[al.First : al.Last+1] {
						p.Subtitles = append(p.Subtitles, panel.GivenLine{Text: l.Text, Start: l.Start, End: l.End + 0.3})
					}
				}
			}
		}
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
