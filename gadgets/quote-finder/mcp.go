package main

// MCP endpoint (streamable HTTP, JSON responses, standard library only) so agents can search
// quotes, read a scene and turn a stretch of it into a Panel Script job.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var mcpVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

const mcpInstructions = `Quote Finder searches the dialogue (subtitles) of the Plex library: TV episodes and movies.

- search_quotes finds where a line is said. Words can be in any order and partly remembered; "exact words" in quotes; regex=true for a regular expression (case-insensitive). Filter with title (a show or movie name from list_titles) or kind (tv|movie). Each hit gives media_id and at_ms.
- get_scene shows the lines around a moment (media_id + at_ms), each with its start time in ms.
- make_panel_script turns the lines from from_ms to to_ms (start times of the first and last line, from get_scene) into a Panel Script job in video-tools: the scene is cut from the file and the subtitle lines become the transcript. It returns the job id and a review link. Then either give the user the link to name the speakers, or use the video-tools MCP (get_job with include_images, then render_panel_transcript) to name them yourself: mouth movement in each line's contact sheet first, voice pitch as a hint, and only name characters you're confident about.
- make_clip cuts the same kind of range out as a video (MP4, plays anywhere), optionally with the subtitles burned in.
- Only part of the library may be indexed yet; list_titles shows coverage.`

type mcpServer struct{ a *app }

type mcpReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *mcpErr         `json:"error,omitempty"`
}

func (m *mcpServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "this MCP endpoint answers POST requests only", http.StatusMethodNotAllowed)
		return
	}
	if o := r.Header.Get("Origin"); o != "" { // DNS-rebinding protection
		if u, err := url.Parse(o); err != nil || u.Host != r.Host {
			http.Error(w, "cross-origin MCP requests are not allowed", http.StatusForbidden)
			return
		}
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	body = bytes.TrimSpace(body)
	write := func(v any) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v)
	}
	if len(body) > 0 && body[0] == '[' {
		var reqs []mcpReq
		if json.Unmarshal(body, &reqs) != nil {
			write(mcpResp{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &mcpErr{-32700, "parse error"}})
			return
		}
		var out []mcpResp
		for _, q := range reqs {
			if len(q.ID) > 0 {
				out = append(out, m.dispatch(r.Context(), q))
			}
		}
		if len(out) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		write(out)
		return
	}
	var q mcpReq
	if json.Unmarshal(body, &q) != nil {
		write(mcpResp{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &mcpErr{-32700, "parse error"}})
		return
	}
	if len(q.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	write(m.dispatch(r.Context(), q))
}

func (m *mcpServer) dispatch(ctx context.Context, q mcpReq) mcpResp {
	resp := mcpResp{JSONRPC: "2.0", ID: q.ID}
	switch q.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		json.Unmarshal(q.Params, &p)
		ver := mcpVersions[0]
		for _, v := range mcpVersions {
			if v == p.ProtocolVersion {
				ver = v
			}
		}
		resp.Result = map[string]any{
			"protocolVersion": ver,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "quote-finder", "title": "Quote Finder", "version": "1.1"},
			"instructions":    mcpInstructions,
		}
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		resp.Result = map[string]any{"tools": mcpTools()}
	case "resources/list":
		resp.Result = map[string]any{"resources": []any{}}
	case "prompts/list":
		resp.Result = map[string]any{"prompts": []any{}}
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if json.Unmarshal(q.Params, &p) != nil {
			resp.Error = &mcpErr{-32602, "invalid params"}
			break
		}
		if len(p.Arguments) == 0 {
			p.Arguments = json.RawMessage("{}")
		}
		resp.Result = m.call(ctx, p.Name, p.Arguments)
	default:
		resp.Error = &mcpErr{-32601, "method not found: " + q.Method}
	}
	return resp
}

func mcpTools() []map[string]any {
	str := func(d string) map[string]any { return map[string]any{"type": "string", "description": d} }
	integer := func(d string) map[string]any { return map[string]any{"type": "integer", "description": d} }
	obj := func(props map[string]any, req ...string) map[string]any {
		o := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
		if len(req) > 0 {
			o["required"] = req
		}
		return o
	}
	ro := map[string]any{"readOnlyHint": true}
	return []map[string]any{
		{
			"name": "search_quotes", "title": "Search quotes", "annotations": ro,
			"description": "Find where a line of dialogue is said in the library. Returns episodes/movies with the matching lines, media_id and at_ms.",
			"inputSchema": obj(map[string]any{
				"query": str("What you remember of the line."),
				"title": str("Only this show or movie (exact name from list_titles)."),
				"kind":  map[string]any{"type": "string", "enum": []string{"tv", "movie"}},
				"regex": map[string]any{"type": "boolean", "description": "Treat query as a regular expression (case-insensitive)."},
				"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "description": "Most hits to return (default 20)."},
			}, "query"),
		},
		{
			"name": "get_scene", "title": "Get scene", "annotations": ro,
			"description": "The lines around a moment, each with its start time (ms). Use the start times of the first and last line you want for make_panel_script.",
			"inputSchema": obj(map[string]any{
				"media_id": integer("From search_quotes."),
				"at_ms":    integer("A moment in the episode or movie, in ms (e.g. a hit's at_ms)."),
				"before":   map[string]any{"type": "integer", "minimum": 0, "maximum": 80, "description": "Lines before (default 12)."},
				"after":    map[string]any{"type": "integer", "minimum": 0, "maximum": 80, "description": "Lines after (default 12)."},
			}, "media_id", "at_ms"),
		},
		{
			"name": "make_panel_script", "title": "Make a panel script",
			"description": "Cuts the scene from the first to the last chosen line out of the media file and starts a Panel Script job in video-tools with those subtitle lines. Returns the job id and review link. At most 60 lines / 5 minutes.",
			"inputSchema": obj(map[string]any{
				"media_id": integer("From search_quotes."),
				"from_ms":  integer("Start time of the first line (from get_scene)."),
				"to_ms":    integer("Start time of the last line (from get_scene)."),
				"panels":   map[string]any{"type": "integer", "minimum": 1, "maximum": 24, "description": "How many panels (default: one per line). Fewer groups lines together; more adds frames in pauses and long lines."},
			}, "media_id", "from_ms", "to_ms"),
		},
		{
			"name": "make_clip", "title": "Make a video clip",
			"description": "Cuts the scene from the first to the last chosen line (plus a second either side) out of the media file and has video-tools make a frame-exact MP4 that plays anywhere. Returns the job id and a link to watch or download it. At most 60 lines / 5 minutes.",
			"inputSchema": obj(map[string]any{
				"media_id":  integer("From search_quotes."),
				"from_ms":   integer("Start time of the first line (from get_scene)."),
				"to_ms":     integer("Start time of the last line (from get_scene)."),
				"subtitles": map[string]any{"type": "boolean", "description": "Burn the subtitle lines into the video (default false)."},
			}, "media_id", "from_ms", "to_ms"),
		},
		{
			"name": "list_titles", "title": "List titles", "annotations": ro,
			"description": "Shows and movies in the library and how many of their files are searchable.",
			"inputSchema": obj(map[string]any{
				"kind": map[string]any{"type": "string", "enum": []string{"tv", "movie"}},
			}),
		},
	}
}

func mcpText(text string, structured any) map[string]any {
	r := map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}
	if structured != nil {
		r["structuredContent"] = structured
	}
	return r
}

func mcpFail(format string, a ...any) map[string]any {
	return map[string]any{"content": []map[string]any{{"type": "text", "text": fmt.Sprintf(format, a...)}}, "isError": true}
}

func label(h hit) string {
	s := h.Title
	if h.Kind == "tv" {
		s += fmt.Sprintf(" S%02dE%02d", h.Season, h.Episode)
		if h.EpTitle != "" {
			s += " “" + h.EpTitle + "”"
		}
	} else if h.Year > 0 {
		s += fmt.Sprintf(" (%d)", h.Year)
	}
	return s
}

func clock(ms int) string {
	s := ms / 1000
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

func (m *mcpServer) call(ctx context.Context, name string, raw json.RawMessage) map[string]any {
	dec := func(v any) error {
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		return d.Decode(v)
	}
	switch name {
	case "search_quotes":
		var a struct {
			Query, Title, Kind string
			Regex              bool
			Limit              int
		}
		if err := dec(&a); err != nil {
			return mcpFail("bad arguments: %v", err)
		}
		if a.Limit <= 0 || a.Limit > 100 {
			a.Limit = 20
		}
		if a.Kind != "tv" && a.Kind != "movie" {
			a.Kind = ""
		}
		res, err := m.a.db.search(a.Query, filter{Kind: a.Kind, Title: a.Title, Regex: a.Regex}, a.Limit)
		if err != nil {
			return mcpFail("%v", err)
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%d hit(s)", len(res.Hits))
		if res.Mode == "any" {
			b.WriteString(" with only some of the words (none had all of them)")
		}
		b.WriteString("\n")
		for _, h := range res.Hits {
			fmt.Fprintf(&b, "\n%s @%s  (media_id %d, at_ms %d)\n", label(h), clock(h.Start), h.MediaID, h.Start)
			for _, l := range h.Lines {
				mark := "  "
				if l.Hit {
					mark = "> "
				}
				fmt.Fprintf(&b, "%s%s  %s\n", mark, clock(l.Start), l.Text)
			}
		}
		return mcpText(b.String(), res)

	case "get_scene":
		var a struct {
			MediaID       int64 `json:"media_id"`
			AtMS          int   `json:"at_ms"`
			Before, After *int
		}
		if err := dec(&a); err != nil {
			return mcpFail("bad arguments: %v", err)
		}
		bf, af := 12, 12
		if a.Before != nil {
			bf = min(max(*a.Before, 0), 80)
		}
		if a.After != nil {
			af = min(max(*a.After, 0), 80)
		}
		lines, err := m.a.db.context(a.MediaID, a.AtMS, bf, af)
		if err != nil {
			return mcpFail("%v", err)
		}
		var b strings.Builder
		for _, l := range lines {
			mark := "  "
			if l.Hit {
				mark = "> "
			}
			fmt.Fprintf(&b, "%s%-8s %8d ms  %s\n", mark, clock(l.Start), l.Start, l.Text)
		}
		return mcpText(b.String(), map[string]any{"lines": lines})

	case "make_panel_script":
		var a struct {
			MediaID int64 `json:"media_id"`
			FromMS  int   `json:"from_ms"`
			ToMS    int   `json:"to_ms"`
			Panels  int   `json:"panels"`
		}
		if err := dec(&a); err != nil {
			return mcpFail("bad arguments: %v", err)
		}
		cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
		res, err := m.a.makeScene(cctx, a.MediaID, a.FromMS, a.ToMS, sceneOpts{Panels: a.Panels})
		if err != nil {
			return mcpFail("%v", err)
		}
		return mcpText(fmt.Sprintf("Panel Script job %s started for %s (%d lines).\nReview and name the speakers: %s\n"+
			"Or with the video-tools MCP: get_job(job_id=%q, include_images=true), then render_panel_transcript.",
			res.JobID, res.Title, res.Lines, res.URL, res.JobID), res)

	case "make_clip":
		var a struct {
			MediaID   int64 `json:"media_id"`
			FromMS    int   `json:"from_ms"`
			ToMS      int   `json:"to_ms"`
			Subtitles bool  `json:"subtitles"`
		}
		if err := dec(&a); err != nil {
			return mcpFail("bad arguments: %v", err)
		}
		cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
		res, err := m.a.makeScene(cctx, a.MediaID, a.FromMS, a.ToMS, sceneOpts{Clip: true, Subtitles: a.Subtitles})
		if err != nil {
			return mcpFail("%v", err)
		}
		return mcpText(fmt.Sprintf("Clip job %s started for %s (%d lines). It takes a few seconds; watch or download it at %s\n"+
			"With the video-tools MCP: get_job(job_id=%q) gives the download link when it's done.", res.JobID, res.Title, res.Lines, res.URL, res.JobID), res)

	case "list_titles":
		var a struct{ Kind string }
		if err := dec(&a); err != nil {
			return mcpFail("bad arguments: %v", err)
		}
		ts, err := m.a.db.titles()
		if err != nil {
			return mcpFail("%v", err)
		}
		var b strings.Builder
		var keep []titleRow
		for _, t := range ts {
			if a.Kind != "" && t.Kind != a.Kind {
				continue
			}
			keep = append(keep, t)
			fmt.Fprintf(&b, "%s [%s] %d/%d searchable\n", t.Title, t.Kind, t.Indexed, t.Files)
		}
		return mcpText(b.String(), map[string]any{"titles": keep})
	}
	return mcpFail("unknown tool %q", name)
}
