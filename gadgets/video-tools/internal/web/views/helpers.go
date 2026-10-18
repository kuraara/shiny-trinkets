package views

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"

	"github.com/kuraara/clowd-docker-stuff/video-tools/internal/jobs"
	"github.com/kuraara/clowd-docker-stuff/video-tools/internal/panel"
	"github.com/kuraara/clowd-docker-stuff/video-tools/internal/storage"
)

// Page carries what the layout needs.
type Page struct {
	Title       string
	Nav         string // home | panel | join | jobs | status
	AuthEnabled bool
	ModelsState string
	Version     string
}

func human(n int64) string { return storage.Human(n) }

func secs(f float64) string { return strconv.FormatFloat(f, 'f', 2, 64) }

func when(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	}
	return t.Format("2 Jan 15:04")
}

func kindLabel(k string) string {
	switch k {
	case "panel":
		return "Panel transcript"
	case "join":
		return "Join"
	}
	return k
}

func statusLabel(s jobs.Status) string {
	switch s {
	case jobs.Queued:
		return "Waiting"
	case jobs.Running:
		return "Working"
	case jobs.Review:
		return "Needs speakers"
	case jobs.Done:
		return "Done"
	case jobs.Failed:
		return "Failed"
	case jobs.Cancelled:
		return "Cancelled"
	}
	return string(s)
}

func jobURL(id string) templ.SafeURL { return templ.SafeURL("/jobs/" + id) }

func fileURL(id, name string) templ.SafeURL {
	return templ.SafeURL("/jobs/" + id + "/files/" + name)
}

func fileURLv(id, name string, t time.Time) templ.SafeURL {
	return templ.SafeURL(fmt.Sprintf("/jobs/%s/files/%s?v=%d", id, name, t.UnixMilli()))
}

func outputsSize(j jobs.Job) int64 {
	var n int64
	for _, o := range j.Outputs {
		n += o.Size
	}
	return n
}

func smallJPEG(name string) string {
	return strings.TrimSuffix(strings.TrimSuffix(name, ".png"), ".jpg") + "_sm.jpg"
}

func pitchText(p *panel.Pitch) string {
	if p == nil {
		return "Voice pitch: not enough voiced sound to measure"
	}
	return fmt.Sprintf("Voice pitch about %d Hz, %s", p.Median, p.Range)
}

func reviewLine(r panel.Review, i int) panel.ReviewLine {
	if i < len(r.Lines) {
		return r.Lines[i]
	}
	return panel.ReviewLine{}
}

func linesOnFrame(r panel.Review, fi int) []panel.ReviewLine {
	var out []panel.ReviewLine
	for _, l := range r.Lines {
		if l.Frame == fi {
			out = append(out, l)
		}
	}
	return out
}

func itoa(i int) string { return strconv.Itoa(i) }

func fileOption(f storage.File) string {
	where := "uploaded"
	if f.Inbox {
		where = "inbox"
	}
	return fmt.Sprintf("%s (%s, %s)", f.Name, human(f.Size), where)
}

func pct(a, b int64) string {
	if b <= 0 {
		return "0"
	}
	return strconv.FormatInt(a*100/b, 10)
}
