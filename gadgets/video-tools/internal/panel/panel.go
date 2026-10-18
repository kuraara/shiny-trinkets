// Package panel runs the panel-transcript worker (analyse → review speakers → render).
package panel

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Pitch struct {
	Median int    `json:"median"`
	P25    int    `json:"p25"`
	P75    int    `json:"p75"`
	Flat   bool   `json:"flat_suspect"`
	Range  string `json:"range"`
}

type Line struct {
	Text         string  `json:"text"`
	Start        float64 `json:"start"`
	End          float64 `json:"end"`
	Timing       string  `json:"timing"`
	Scenes       []int   `json:"scenes"`
	Frame        int     `json:"frame"`
	Pitch        *Pitch  `json:"pitch_hz,omitempty"`
	ContactSheet string  `json:"contact_sheet"`
}

type Frame struct {
	T     float64 `json:"t"`
	Image string  `json:"image"`
	Scene int     `json:"scene"`
	Lines []int   `json:"lines"`
}

type Analysis struct {
	Duration  float64   `json:"duration"`
	Mode      string    `json:"mode"`
	SceneCuts []float64 `json:"scene_cuts"`
	Lines     []Line    `json:"lines"`
	Frames    []Frame   `json:"frames"`
	Warnings  []string  `json:"warnings"`
}

// Params are what the user chose when starting the job.
type Params struct {
	Times          []float64 `json:"times,omitempty"` // empty = auto
	SceneThreshold float64   `json:"scene_threshold,omitempty"`
}

// ReviewLine is the user's (or agent's) decision for one line.
type ReviewLine struct {
	Speaker string `json:"speaker"`
	Text    string `json:"text"`
	Frame   int    `json:"frame"` // 0-based panel index; -1 leaves the line off the image
}

type Review struct {
	Lines []ReviewLine `json:"lines"`
}

// Worker holds how to call the python scripts.
type Worker struct {
	Python, Dir, ModelsDir, FontsDir string
	ASRThreads, FFThreads, MaxSecs   int
}

func (w Worker) env() []string {
	return append(os.Environ(), "MODELS_DIR="+w.ModelsDir, "FONTS_DIR="+w.FontsDir,
		"ASR_THREADS="+strconv.Itoa(w.ASRThreads), "FFMPEG_THREADS="+strconv.Itoa(w.FFThreads),
		"MAX_TRANSCRIPT_SECONDS="+strconv.Itoa(w.MaxSecs), "OMP_NUM_THREADS="+strconv.Itoa(w.ASRThreads))
}

// run executes a worker script, streaming "PROGRESS n msg" lines to progress and returning the
// script's "ERROR ..." line as the error.
func (w Worker) run(ctx context.Context, progress func(int, string), logf func(string), script string, args ...string) error {
	cmd := exec.CommandContext(ctx, w.Python, append([]string{"-I", filepath.Join(w.Dir, script)}, args...)...)
	cmd.Env = w.env()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("cannot start worker: %w", err)
	}
	var lastErr, tail string
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		l := sc.Text()
		switch {
		case strings.HasPrefix(l, "PROGRESS "):
			f := strings.SplitN(l, " ", 3)
			if len(f) == 3 && progress != nil {
				n, _ := strconv.Atoi(f[1])
				progress(n, f[2])
			}
		case strings.HasPrefix(l, "ERROR "):
			lastErr = strings.TrimPrefix(l, "ERROR ")
		default:
			if strings.Contains(strings.ToLower(l), "warning") { // library noise
				continue
			}
			tail = l
			if logf != nil {
				logf(l)
			}
		}
	}
	err = cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		if lastErr == "" {
			lastErr = tail
		}
		return fmt.Errorf("%s", lastErr)
	}
	return nil
}

// Analyse runs analyse.py for clip into outDir and writes the default review.
func (w Worker) Analyse(ctx context.Context, clip, outDir string, p Params, progress func(int, string), logf func(string)) (Analysis, error) {
	args := []string{clip, outDir}
	if len(p.Times) > 0 {
		args = append(args, "--times")
		for _, t := range p.Times {
			args = append(args, strconv.FormatFloat(t, 'f', 3, 64))
		}
	}
	if p.SceneThreshold > 0 {
		args = append(args, "--scene-threshold", strconv.FormatFloat(p.SceneThreshold, 'f', 3, 64))
	}
	if err := w.run(ctx, progress, logf, "analyse.py", args...); err != nil {
		return Analysis{}, err
	}
	a, err := LoadAnalysis(outDir)
	if err != nil {
		return a, err
	}
	r := Review{}
	for _, l := range a.Lines {
		r.Lines = append(r.Lines, ReviewLine{Text: l.Text, Frame: l.Frame})
	}
	return a, SaveReview(outDir, r)
}

func LoadAnalysis(dir string) (Analysis, error) {
	var a Analysis
	b, err := os.ReadFile(filepath.Join(dir, "analysis.json"))
	if err != nil {
		return a, fmt.Errorf("analysis not available: %w", err)
	}
	return a, json.Unmarshal(b, &a)
}

func LoadReview(dir string) (Review, error) {
	var r Review
	b, err := os.ReadFile(filepath.Join(dir, "review.json"))
	if err != nil {
		return r, err
	}
	return r, json.Unmarshal(b, &r)
}

func SaveReview(dir string, r Review) error {
	b, _ := json.MarshalIndent(r, "", " ")
	return os.WriteFile(filepath.Join(dir, "review.json"), b, 0o644)
}

// Merge applies a review onto the analysis, validating indexes. Empty speakers/texts fall back
// to the existing values where sensible.
func Merge(a Analysis, prev Review, upd Review) (Review, error) {
	if len(upd.Lines) != len(a.Lines) {
		return prev, fmt.Errorf("expected %d lines, got %d", len(a.Lines), len(upd.Lines))
	}
	out := Review{Lines: make([]ReviewLine, len(upd.Lines))}
	for i, l := range upd.Lines {
		if l.Frame < -1 || l.Frame >= len(a.Frames) {
			return prev, fmt.Errorf("line %d: panel %d doesn't exist (1-%d, or 0 to leave it out)", i+1, l.Frame+1, len(a.Frames))
		}
		l.Speaker = strings.TrimSpace(l.Speaker)
		l.Text = strings.TrimSpace(l.Text)
		if l.Text == "" {
			l.Text = a.Lines[i].Text
		}
		out.Lines[i] = l
	}
	return out, nil
}

// Render draws the strip and writes transcript exports. Returns the output file names.
func (w Worker) Render(ctx context.Context, outDir string, a Analysis, r Review) ([]string, error) {
	type specLine struct {
		Speaker string `json:"speaker"`
		Text    string `json:"text"`
	}
	type specPanel struct {
		Image string     `json:"image"`
		Lines []specLine `json:"lines"`
	}
	spec := struct {
		Out    string      `json:"out"`
		Panels []specPanel `json:"panels"`
	}{Out: filepath.Join(outDir, "panel_transcript.png")}
	for fi, f := range a.Frames {
		p := specPanel{Image: filepath.Join(outDir, f.Image), Lines: []specLine{}}
		for _, l := range r.Lines {
			if l.Frame == fi {
				p.Lines = append(p.Lines, specLine{l.Speaker, l.Text})
			}
		}
		spec.Panels = append(spec.Panels, p)
	}
	b, _ := json.Marshal(spec)
	specPath := filepath.Join(outDir, "render_spec.json")
	if err := os.WriteFile(specPath, b, 0o644); err != nil {
		return nil, err
	}
	if err := w.run(ctx, nil, nil, "render.py", specPath); err != nil {
		return nil, err
	}
	if err := writeTranscripts(outDir, a, r); err != nil {
		return nil, err
	}
	return []string{"panel_transcript.png", "transcript.md", "transcript.json"}, nil
}

func writeTranscripts(dir string, a Analysis, r Review) error {
	var md strings.Builder
	md.WriteString("| Time | Speaker | Line |\n|---|---|---|\n")
	type row struct {
		Start   float64 `json:"start"`
		End     float64 `json:"end"`
		Speaker string  `json:"speaker"`
		Text    string  `json:"text"`
		Panel   int     `json:"panel"` // 1-based, 0 = not on the image
	}
	var rows []row
	for i, l := range a.Lines {
		rl := r.Lines[i]
		sp := rl.Speaker
		if sp == "" {
			sp = "—"
		}
		fmt.Fprintf(&md, "| %s–%ss | %s | \"%s\" |\n", ff(l.Start), ff(l.End), mdEsc(sp), mdEsc(rl.Text))
		rows = append(rows, row{l.Start, l.End, rl.Speaker, rl.Text, rl.Frame + 1})
	}
	if err := os.WriteFile(filepath.Join(dir, "transcript.md"), []byte(md.String()), 0o644); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(rows, "", " ")
	return os.WriteFile(filepath.Join(dir, "transcript.json"), b, 0o644)
}

func ff(f float64) string { return strconv.FormatFloat(f, 'f', 2, 64) }

func mdEsc(s string) string { return strings.ReplaceAll(s, "|", "\\|") }

var timeRe = regexp.MustCompile(`^(?:(\d+):)?(?:(\d+):)?(\d+(?:\.\d+)?)s?$`)

// ParseTimes accepts "1.0, 2.5, 7.2", "0:07.2", "1:02:03" or "7.2s" separated by commas or spaces.
func ParseTimes(s string) ([]float64, error) {
	var out []float64
	for _, tok := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == ';' || r == '\n' }) {
		m := timeRe.FindStringSubmatch(strings.TrimSpace(tok))
		if m == nil {
			return nil, fmt.Errorf("can't read %q as a time (use seconds like 7.2 or m:ss like 0:07.2)", tok)
		}
		var h, mi float64
		sec, _ := strconv.ParseFloat(m[3], 64)
		switch {
		case m[1] != "" && m[2] != "":
			h, _ = strconv.ParseFloat(m[1], 64)
			mi, _ = strconv.ParseFloat(m[2], 64)
		case m[1] != "":
			mi, _ = strconv.ParseFloat(m[1], 64)
		}
		out = append(out, h*3600+mi*60+sec)
	}
	if len(out) > 24 {
		return nil, fmt.Errorf("at most 24 panels")
	}
	return out, nil
}
