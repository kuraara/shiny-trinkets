package main

// MCP endpoint (streamable HTTP, JSON responses, standard library only), the same transport as
// Ask Me's and Quote Finder's.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
)

var mcpVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

const mcpInstructions = `Subtitle Writer puts subtitle files next to videos in the Plex library on the Pi, so Plex offers them as subtitle tracks.

- put_subtitle: video (path relative to the media root, as Quote Finder reports it, e.g. "TvShows/Show/Season 1/Show.S01E01.mkv"), srt (the whole SRT text), label (lowercase word saying where it came from: "whisper" for speech-to-text, "translated" for a copy with foreign dialogue translated), lang (default "en"). The file is written as "<video name>.<label>.<lang>.srt" next to the video.
- list_subtitles: what this service has written (optionally filtered by a path substring or label).
- remove_subtitle: delete one it wrote (by the path list_subtitles gives).

It never writes anything but .srt files, never outside the media root, and never replaces or removes a file it didn't write. Writing the same video+label+lang again replaces its own earlier file. After adding files, Plex picks them up on its next scan of that folder.`

type mcpServer struct{ w *writer }

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
	if o := r.Header.Get("Origin"); o != "" { // DNS-rebinding protection
		if u, err := url.Parse(o); err != nil || u.Host != r.Host {
			http.Error(w, "cross-origin MCP requests are not allowed", http.StatusForbidden)
			return
		}
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 2*maxSRT))
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
			"serverInfo":      map[string]any{"name": "subtitle-writer", "title": "Subtitle Writer", "version": "1.0"},
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
		resp.Result = m.call(p.Name, p.Arguments)
	default:
		resp.Error = &mcpErr{-32601, "method not found: " + q.Method}
	}
	return resp
}

func mcpTools() []map[string]any {
	str := func(d string) map[string]any { return map[string]any{"type": "string", "description": d} }
	obj := func(props map[string]any, req ...string) map[string]any {
		o := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
		if len(req) > 0 {
			o["required"] = req
		}
		return o
	}
	return []map[string]any{
		{
			"name": "put_subtitle", "title": "Put a subtitle next to a video",
			"description": "Write an SRT subtitle next to a video as <video name>.<label>.<lang>.srt. Replaces only a file this service wrote earlier for the same video, label and language. Returns the file's path and size.",
			"inputSchema": obj(map[string]any{
				"video": str("The video's path relative to the media root, e.g. TvShows/Show/Season 1/Show.S01E01.mkv."),
				"srt":   str("The whole subtitle file in SRT format (UTF-8)."),
				"label": str("Where it came from, lowercase letters/digits: whisper, translated, ..."),
				"lang":  str("ISO 639 language code of the text (default en)."),
			}, "video", "srt", "label"),
		},
		{
			"name": "list_subtitles", "title": "List written subtitles", "annotations": map[string]any{"readOnlyHint": true},
			"description": "The subtitle files this service has written, with their video, label, size and time.",
			"inputSchema": obj(map[string]any{"match": str("Only paths containing this (case-insensitive)."), "label": str("Only this label.")}),
		},
		{
			"name": "remove_subtitle", "title": "Remove a written subtitle", "annotations": map[string]any{"destructiveHint": true},
			"description": "Delete a subtitle file this service wrote (and that hasn't been edited since). Refuses any other file.",
			"inputSchema": obj(map[string]any{"path": str("The subtitle's path as list_subtitles gives it.")}, "path"),
		},
	}
}

func textResult(v any, isErr bool) map[string]any {
	b, _ := json.MarshalIndent(v, "", "  ")
	return map[string]any{"content": []map[string]any{{"type": "text", "text": string(b)}}, "isError": isErr}
}

func errResult(err error) map[string]any {
	return textResult(map[string]string{"error": err.Error()}, true)
}

func (m *mcpServer) call(name string, args json.RawMessage) map[string]any {
	switch name {
	case "put_subtitle":
		var a putReq
		if err := json.Unmarshal(args, &a); err != nil {
			return errResult(err)
		}
		e, err := m.w.Put(a.Video, a.SRT, a.Label, a.Lang)
		if err != nil {
			return errResult(err)
		}
		return textResult(e, false)
	case "list_subtitles":
		var a struct{ Match, Label string }
		json.Unmarshal(args, &a)
		list := m.w.man.List(a.Match, a.Label)
		return textResult(map[string]any{"count": len(list), "subtitles": list}, false)
	case "remove_subtitle":
		var a struct{ Path string }
		json.Unmarshal(args, &a)
		e, err := m.w.Remove(a.Path)
		if err != nil {
			return errResult(err)
		}
		return textResult(map[string]any{"removed": e}, false)
	}
	return textResult(map[string]string{"error": "unknown tool " + name}, true)
}
