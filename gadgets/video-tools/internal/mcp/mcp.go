// Package mcp serves a Model Context Protocol endpoint (streamable HTTP, stateless JSON responses)
// so agents can make panel transcripts and join clips. Standard library only.
package mcp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/kuraara/clowd-docker-stuff/video-tools/internal/app"
	"github.com/kuraara/clowd-docker-stuff/video-tools/internal/jobs"
	"github.com/kuraara/clowd-docker-stuff/video-tools/internal/panel"
	"github.com/kuraara/clowd-docker-stuff/video-tools/internal/web"
)

var protocolVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

const instructions = `Video tools makes panel transcripts (stills from a clip, stacked vertically on black, each with its dialogue burned in as a subtitle "Name: “line”") and joins clips end to end.

GETTING CLIPS IN
- Upload over HTTP: curl -F file=@clip.mov <base>/api/files  (add -H "Authorization: Bearer <token>" if the server has AUTH_TOKEN). The response gives each file's id (f_...).
- Or the user drops files in the server's inbox folder; they appear in list_clips as inbox:<path>.

PANEL TRANSCRIPT
1. start_panel_transcript(file_id, times?) — times in seconds; omit for one panel per spoken line. It waits for the analysis (roughly 5x the clip length on CPU) and returns the transcript lines, the frames and, for each line, a contact sheet of eight frames spanning the line.
2. Work out who says each line. Primary evidence: whose mouth opens and closes across that line's contact sheet (judge from the full frames). Supporting evidence: voice pitch — children ~300–500 Hz, adult women ~165–300 Hz, adult men ~85–180 Hz (rough). Context: who holds or does what is being talked about.
   - Fictional or animated characters: name them only when look, setting and dialogue agree; otherwise say you're unsure.
   - Real people: never identify anyone from their face. Use names the user gave you, on-screen captions or name tags; otherwise "Speaker 1", "Speaker 2" and ask the user.
   - Nobody on screen talking: use "(off-screen)" or ask.
   Merge consecutive sentences from the same speaker only if the user wants fewer subtitles; otherwise keep one entry per line.
3. render_panel_transcript(job_id, lines=[{speaker, text?, panel?}, ...]) — exactly one entry per transcript line, in order. panel is 1-based; 0 leaves a line off the image; omit to keep the suggested panel.
4. Give the user the PNG link and the transcript as a verbatim table (time, speaker, line). Say which speakers you were unsure of, and if a panel's timestamp falls inside one line but carries the next, say so.

JOINING CLIPS
- join_clips(file_ids in order, transition none|fade|crossfade, seconds?). With no transition and matching clips it joins without re-encoding (fast, lossless); otherwise it converts everything to H.264/AAC in the first clip's size. The result lists why re-encoding was needed.

Jobs and files are deleted automatically after the server's retention period (see list_clips output).`

type Server struct {
	svc *app.Service
}

func New(svc *app.Service) *Server { return &Server{svc: svc} }

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcErr         `json:"error,omitempty"`
}

type content struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

type toolResult struct {
	Content    []content `json:"content"`
	IsError    bool      `json:"isError,omitempty"`
	Structured any       `json:"structuredContent,omitempty"`
}

func (m *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "this MCP endpoint answers POST requests only (no server-sent event stream)", http.StatusMethodNotAllowed)
		return
	}
	// browsers on other sites must not be able to drive the server (DNS-rebinding protection)
	if o := r.Header.Get("Origin"); o != "" {
		if u, err := url.Parse(o); err != nil || u.Host != r.Host {
			http.Error(w, "cross-origin MCP requests are not allowed", http.StatusForbidden)
			return
		}
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	body = bytes.TrimSpace(body)
	write := func(v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	if len(body) > 0 && body[0] == '[' { // batch (older protocol versions)
		var reqs []rpcReq
		if err := json.Unmarshal(body, &reqs); err != nil {
			write(rpcResp{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcErr{-32700, "parse error"}})
			return
		}
		var out []rpcResp
		for _, q := range reqs {
			if len(q.ID) > 0 {
				out = append(out, m.dispatch(r, q))
			}
		}
		if len(out) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		write(out)
		return
	}
	var q rpcReq
	if err := json.Unmarshal(body, &q); err != nil {
		write(rpcResp{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcErr{-32700, "parse error"}})
		return
	}
	if len(q.ID) == 0 { // notification
		w.WriteHeader(http.StatusAccepted)
		return
	}
	write(m.dispatch(r, q))
}

func (m *Server) dispatch(r *http.Request, q rpcReq) rpcResp {
	resp := rpcResp{JSONRPC: "2.0", ID: q.ID}
	switch q.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(q.Params, &p)
		ver := protocolVersions[0]
		for _, v := range protocolVersions {
			if v == p.ProtocolVersion {
				ver = v
			}
		}
		resp.Result = map[string]any{
			"protocolVersion": ver,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "video-tools", "title": "Video tools", "version": app.Version},
			"instructions":    strings.ReplaceAll(instructions, "<base>", web.BaseURL(m.svc.Cfg.PublicURL, r)),
		}
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		resp.Result = map[string]any{"tools": tools()}
	case "resources/list":
		resp.Result = map[string]any{"resources": []any{}}
	case "prompts/list":
		resp.Result = map[string]any{"prompts": []any{}}
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(q.Params, &p); err != nil {
			resp.Error = &rpcErr{-32602, "invalid params"}
			break
		}
		if len(p.Arguments) == 0 {
			p.Arguments = json.RawMessage("{}")
		}
		resp.Result = m.call(r, p.Name, p.Arguments)
	default:
		resp.Error = &rpcErr{-32601, "method not found: " + q.Method}
	}
	return resp
}

// ---------------------------------------------------------------- tool definitions

func obj(props map[string]any, required ...string) map[string]any {
	o := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		o["required"] = required
	}
	return o
}

func tools() []map[string]any {
	str := func(d string) map[string]any { return map[string]any{"type": "string", "description": d} }
	num := func(d string) map[string]any { return map[string]any{"type": "number", "description": d} }
	wait := map[string]any{"type": "integer", "minimum": 0, "maximum": 900,
		"description": "Seconds to wait for the job to finish before returning (default 300). 0 returns at once."}
	return []map[string]any{
		{
			"name":        "list_clips",
			"title":       "List clips",
			"description": "Lists clips available as inputs: uploaded files (ids f_...) and inbox files (ids inbox:<path>). Also says how to upload and how long files are kept.",
			"inputSchema": obj(map[string]any{}),
			"annotations": map[string]any{"readOnlyHint": true},
		},
		{
			"name":  "start_panel_transcript",
			"title": "Start a panel transcript",
			"description": "Analyses a clip: transcribes it, finds scene cuts, takes the frames, and maps each line to a panel. " +
				"Returns the lines with timings and voice pitch, the frame images and a contact sheet per line for identifying speakers. " +
				"Next step: render_panel_transcript with a speaker for every line.",
			"inputSchema": obj(map[string]any{
				"file_id":         str("Clip id from list_clips or an upload (f_... or inbox:...)."),
				"times":           map[string]any{"type": "array", "items": map[string]any{"type": "number", "minimum": 0}, "description": "Seconds into the clip to take each panel from. Omit for one panel per spoken line."},
				"scene_threshold": num("Scene-cut sensitivity 0.05–0.95, default 0.3. Lower finds more cuts."),
				"wait_seconds":    wait,
			}, "file_id"),
		},
		{
			"name":        "get_job",
			"title":       "Get job",
			"description": "Status, progress, notes and download links for a job. For a panel transcript awaiting speakers it also returns the lines; set include_images to get the frames and contact sheets again.",
			"inputSchema": obj(map[string]any{
				"job_id":         str("Job id (j_...)."),
				"wait_seconds":   wait,
				"include_images": map[string]any{"type": "boolean", "description": "Panel jobs: include frame and contact-sheet images (default false)."},
			}, "job_id"),
			"annotations": map[string]any{"readOnlyHint": true},
		},
		{
			"name":        "get_panel_images",
			"title":       "Get panel images",
			"description": "Returns specific frame images and/or line contact sheets of a panel-transcript job (use when there were too many to include earlier).",
			"inputSchema": obj(map[string]any{
				"job_id": str("Job id."),
				"lines":  map[string]any{"type": "array", "items": map[string]any{"type": "integer", "minimum": 1}, "description": "1-based line numbers whose contact sheets you want."},
				"panels": map[string]any{"type": "array", "items": map[string]any{"type": "integer", "minimum": 1}, "description": "1-based panel numbers whose frames you want."},
			}, "job_id"),
			"annotations": map[string]any{"readOnlyHint": true},
		},
		{
			"name":  "render_panel_transcript",
			"title": "Render a panel transcript",
			"description": "Draws the finished image: frames stacked on black, each line burned in as `Speaker: “line”`. " +
				"Send exactly one entry per transcript line, in order. Can be called again to change names or text.",
			"inputSchema": obj(map[string]any{
				"job_id": str("Job id from start_panel_transcript."),
				"lines": map[string]any{"type": "array", "description": "One entry per transcript line, in order.",
					"items": obj(map[string]any{
						"speaker": str("Who says the line. Empty string prints the line without a name."),
						"text":    str("Corrected wording; omit to keep the transcription."),
						"panel":   map[string]any{"type": "integer", "minimum": 0, "description": "1-based panel to show the line on; 0 leaves it off the image; omit to keep the suggestion."},
					}, "speaker")},
			}, "job_id", "lines"),
		},
		{
			"name":  "join_clips",
			"title": "Join clips",
			"description": "Joins clips end to end in the given order. With transition none and matching clips it stream-copies (fast, lossless); " +
				"otherwise it re-encodes everything to H.264/AAC at the first clip's size. Returns the download link when finished.",
			"inputSchema": obj(map[string]any{
				"file_ids":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": 2, "description": "Clip ids in join order."},
				"transition":   map[string]any{"type": "string", "enum": []string{"none", "fade", "crossfade"}, "description": "none (default), fade (through black) or crossfade."},
				"seconds":      num("Transition length, default 0.5, max 5."),
				"mode":         map[string]any{"type": "string", "enum": []string{"auto", "copy", "reencode"}, "description": "auto (default): copy when possible; copy: fail rather than re-encode; reencode: always."},
				"fade_edges":   map[string]any{"type": "boolean", "description": "Also fade in at the start and out at the end."},
				"name":         str("Output file name without extension (default joined)."),
				"wait_seconds": wait,
			}, "file_ids"),
		},
		{
			"name":        "cancel_job",
			"title":       "Cancel job",
			"description": "Stops a queued or running job.",
			"inputSchema": obj(map[string]any{"job_id": str("Job id.")}, "job_id"),
			"annotations": map[string]any{"destructiveHint": true},
		},
	}
}

// ---------------------------------------------------------------- tool calls

func errResult(format string, a ...any) toolResult {
	return toolResult{IsError: true, Content: []content{{Type: "text", Text: fmt.Sprintf(format, a...)}}}
}

func textResult(s string, structured any) toolResult {
	return toolResult{Content: []content{{Type: "text", Text: s}}, Structured: structured}
}

func (m *Server) call(r *http.Request, name string, raw json.RawMessage) toolResult {
	ctx := r.Context()
	base := web.BaseURL(m.svc.Cfg.PublicURL, r)
	dec := func(v any) error {
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		return d.Decode(v)
	}
	switch name {
	case "list_clips":
		files := m.svc.Store.List()
		var b strings.Builder
		fmt.Fprintf(&b, "%d clip(s).\n", len(files))
		for _, f := range files {
			src := "upload"
			if f.Inbox {
				src = "inbox"
			}
			fmt.Fprintf(&b, "- %s  %s  (%s, %s)\n", f.ID, f.Name, humanSize(f.Size), src)
		}
		fmt.Fprintf(&b, "\nUpload more with: curl -F file=@clip.mov %s/api/files", base)
		if m.svc.Cfg.AuthToken != "" {
			b.WriteString(` -H "Authorization: Bearer <token>"`)
		}
		fmt.Fprintf(&b, "\nUploads and jobs are deleted after %s.", m.svc.Cfg.JobTTL)
		return textResult(b.String(), map[string]any{"files": files})

	case "start_panel_transcript":
		var a struct {
			FileID         string    `json:"file_id"`
			Times          []float64 `json:"times"`
			SceneThreshold float64   `json:"scene_threshold"`
			Wait           *int      `json:"wait_seconds"`
		}
		if err := dec(&a); err != nil {
			return errResult("bad arguments: %v", err)
		}
		j, err := m.svc.StartPanel(a.FileID, panel.Params{Times: a.Times, SceneThreshold: a.SceneThreshold})
		if err != nil {
			return errResult("%v", err)
		}
		j = m.wait(ctx, j.ID, waitFor(a.Wait))
		return m.jobResult(base, j, true)

	case "get_job":
		var a struct {
			JobID  string `json:"job_id"`
			Wait   *int   `json:"wait_seconds"`
			Images bool   `json:"include_images"`
		}
		if err := dec(&a); err != nil {
			return errResult("bad arguments: %v", err)
		}
		w := 0
		if a.Wait != nil {
			w = *a.Wait
		}
		j := m.wait(ctx, a.JobID, time.Duration(min(max(w, 0), 900))*time.Second)
		if j.ID == "" {
			return errResult("job %q not found", a.JobID)
		}
		return m.jobResult(base, j, a.Images)

	case "get_panel_images":
		var a struct {
			JobID  string `json:"job_id"`
			Lines  []int  `json:"lines"`
			Panels []int  `json:"panels"`
		}
		if err := dec(&a); err != nil {
			return errResult("bad arguments: %v", err)
		}
		pv, err := m.svc.PanelState(a.JobID)
		if err != nil {
			return errResult("%v", err)
		}
		res := toolResult{}
		for _, p := range a.Panels {
			if p < 1 || p > len(pv.Analysis.Frames) {
				return errResult("panel %d doesn't exist (1-%d)", p, len(pv.Analysis.Frames))
			}
			f := pv.Analysis.Frames[p-1]
			m.addImage(&res, a.JobID, small(f.Image), fmt.Sprintf("Panel %d, taken at %.2fs:", p, f.T))
		}
		for _, l := range a.Lines {
			if l < 1 || l > len(pv.Analysis.Lines) {
				return errResult("line %d doesn't exist (1-%d)", l, len(pv.Analysis.Lines))
			}
			ln := pv.Analysis.Lines[l-1]
			m.addImage(&res, a.JobID, small(ln.ContactSheet), fmt.Sprintf("Line %d contact sheet, eight frames across %.2f–%.2fs (“%s”):", l, ln.Start, ln.End, ln.Text))
		}
		if len(res.Content) == 0 {
			return errResult("ask for at least one line or panel")
		}
		return res

	case "render_panel_transcript":
		var a struct {
			JobID string `json:"job_id"`
			web.RenderRequest
		}
		if err := dec(&a); err != nil {
			return errResult("bad arguments: %v", err)
		}
		rv, err := web.ToReview(m.svc, a.JobID, a.RenderRequest)
		if err != nil {
			return errResult("%v", err)
		}
		rctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		j, err := m.svc.RenderPanel(rctx, a.JobID, rv)
		if err != nil {
			return errResult("%v", err)
		}
		d, _ := web.Detail(m.svc, base, j.ID)
		pv, _ := m.svc.PanelState(j.ID)
		var b strings.Builder
		fmt.Fprintf(&b, "Rendered. Image: %s\nTranscript: %s\n\n| Time | Speaker | Line |\n|---|---|---|\n",
			d.Files["panel_transcript.png"], d.Files["transcript.md"])
		for i, l := range pv.Analysis.Lines {
			rl := pv.Review.Lines[i]
			fmt.Fprintf(&b, "| %.2f–%.2fs | %s | \"%s\" |\n", l.Start, l.End, rl.Speaker, rl.Text)
		}
		res := textResult(b.String(), d)
		m.addImage(&res, j.ID, "panel_transcript_sm.jpg", "Preview of the finished image:")
		return res

	case "join_clips":
		var a struct {
			app.JoinRequest
			Wait *int `json:"wait_seconds"`
		}
		if err := dec(&a); err != nil {
			return errResult("bad arguments: %v", err)
		}
		j, err := m.svc.StartJoin(a.JoinRequest)
		if err != nil {
			return errResult("%v", err)
		}
		j = m.wait(ctx, j.ID, waitFor(a.Wait))
		return m.jobResult(base, j, false)

	case "cancel_job":
		var a struct {
			JobID string `json:"job_id"`
		}
		if err := dec(&a); err != nil {
			return errResult("bad arguments: %v", err)
		}
		if err := m.svc.Jobs.Cancel(a.JobID); err != nil {
			return errResult("%v", err)
		}
		return textResult("Cancelled "+a.JobID+".", nil)
	}
	return errResult("unknown tool %q", name)
}

func waitFor(w *int) time.Duration {
	if w == nil {
		return 300 * time.Second
	}
	return time.Duration(min(max(*w, 0), 900)) * time.Second
}

// wait polls until the job leaves the queue/run state, the timeout passes or the client goes away.
func (m *Server) wait(ctx context.Context, id string, d time.Duration) jobs.Job {
	deadline := time.Now().Add(d)
	for {
		j, err := m.svc.Jobs.Get(id)
		if err != nil || !j.Status.Active() || time.Now().After(deadline) {
			return j
		}
		select {
		case <-ctx.Done():
			return j
		case <-time.After(time.Second):
		}
	}
}

func (m *Server) jobResult(base string, j jobs.Job, images bool) toolResult {
	d, err := web.Detail(m.svc, base, j.ID)
	if err != nil {
		return errResult("%v", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Job %s (%s): %s", j.ID, j.Kind, j.Status)
	switch {
	case j.Status.Active():
		fmt.Fprintf(&b, ", %d%% — %s. Still working; call get_job with wait_seconds to keep waiting.\n", j.Progress, j.Message)
	case j.Status == jobs.Failed:
		return toolResult{IsError: true, Structured: d, Content: []content{{Type: "text", Text: b.String() + ": " + j.Error}}}
	case j.Status == jobs.Cancelled:
		b.WriteString(".\n")
	default:
		b.WriteString(".\n")
	}
	for _, n := range j.Notes {
		b.WriteString("Note: " + n + "\n")
	}
	for _, o := range j.Outputs {
		fmt.Fprintf(&b, "%s: %s (%s)\n", o.Label, d.Files[o.Name], humanSize(o.Size))
	}
	res := toolResult{Structured: d}
	if p := d.Panel; p != nil {
		fmt.Fprintf(&b, "\nClip %.2fs, scene cuts at %v. %d panel(s):\n", p.Duration, p.SceneCuts, len(p.Frames))
		for _, f := range p.Frames {
			fmt.Fprintf(&b, "- Panel %d at %.2fs\n", f.Panel, f.Time)
		}
		fmt.Fprintf(&b, "\n%d line(s):\n", len(p.Lines))
		for i, l := range p.Lines {
			pitch := "pitch: not measurable"
			if l.Pitch != nil {
				pitch = fmt.Sprintf("pitch ~%d Hz, %s", l.Pitch.Median, l.Pitch.Range)
				if l.Pitch.Flat {
					pitch += " (suspiciously steady: likely music or a sound effect, ignore it)"
				}
			}
			cur := ""
			if i < len(p.Review) && p.Review[i].Speaker != "" {
				cur = " speaker so far: " + p.Review[i].Speaker + ";"
			}
			fmt.Fprintf(&b, "- Line %d, %.2f–%.2fs, on panel %d;%s %s: “%s”\n", l.Line, l.Start, l.End, l.Panel, cur, pitch, l.Text)
		}
		if j.Status == jobs.Review {
			fmt.Fprintf(&b, "\nNext: decide each line's speaker (see the instructions on identifying speakers), then call "+
				"render_panel_transcript with job_id %q and exactly %d entries in lines.\n", j.ID, len(p.Lines))
		}
		res.Content = append(res.Content, content{Type: "text", Text: b.String()})
		if images {
			pv, _ := m.svc.PanelState(j.ID)
			for i, f := range pv.Analysis.Frames {
				if i >= 10 {
					res.Content = append(res.Content, content{Type: "text", Text: "More panels: use get_panel_images."})
					break
				}
				m.addImage(&res, j.ID, small(f.Image), fmt.Sprintf("Panel %d, taken at %.2fs:", i+1, f.T))
			}
			for i, l := range pv.Analysis.Lines {
				if i >= 12 {
					res.Content = append(res.Content, content{Type: "text", Text: "More contact sheets: use get_panel_images."})
					break
				}
				m.addImage(&res, j.ID, small(l.ContactSheet), fmt.Sprintf("Line %d contact sheet, eight frames across %.2f–%.2fs (“%s”):", i+1, l.Start, l.End, l.Text))
			}
		}
		return res
	}
	res.Content = append(res.Content, content{Type: "text", Text: b.String()})
	return res
}

func small(name string) string {
	name = strings.TrimSuffix(strings.TrimSuffix(name, ".png"), ".jpg")
	return name + "_sm.jpg"
}

func (m *Server) addImage(res *toolResult, jobID, name, label string) {
	p, err := m.svc.OutputPath(jobID, name)
	if err != nil {
		return
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	res.Content = append(res.Content, content{Type: "text", Text: label},
		content{Type: "image", Data: base64.StdEncoding.EncodeToString(b), MimeType: "image/jpeg"})
}

func humanSize(n int64) string {
	const u = 1024
	if n < u {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(u), 0
	for m := n / u; m >= u; m /= u {
		div *= u
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
