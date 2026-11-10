package main

// MCP endpoint (streamable HTTP, JSON responses, standard library only), the same shape as
// Quote Finder's.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

var mcpVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

const mcpInstructions = `Plan Runner carries out a build plan on a build server: it clones the repository into a fresh workspace, has an agent work through the plan (building and testing as it goes, committing per phase), pushes the commits and keeps a log. One job runs at a time; others queue.

- submit_plan: repo (https git URL), plan (the whole plan as Markdown), optionally branch (default: the repo's default branch), title and push (default true). Returns the job id.
- get_job: status (queued, running, done, failed, cancelled), the current step, the commits made, and the last lines of the log. Poll it every minute or two while a job runs; jobs take minutes to hours.
- get_log: more of the log, by line number.
- list_jobs, cancel_job.

Write the plan so it stands on its own: the agent sees only the plan and the repository. Say how to build and test, and what "done" looks like. The agent commits but never pushes; the runner pushes once it finishes, to the same branch.`

type mcpServer struct{ r *runner }

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
			"serverInfo":      map[string]any{"name": "plan-runner", "title": "Plan Runner", "version": "1.0"},
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
			"name": "submit_plan", "title": "Submit a plan",
			"description": "Queue a build plan for a repository. Returns the job id and how many jobs are ahead of it.",
			"inputSchema": obj(map[string]any{
				"repo":   str("The repository's https git URL."),
				"plan":   str("The whole plan, as Markdown. It must stand on its own."),
				"branch": str("Branch to work on and push to (default: the repo's default branch)."),
				"title":  str("A short name for the job (default: the plan's first line)."),
				"push":   map[string]any{"type": "boolean", "description": "Push the commits when the job finishes (default true)."},
			}, "repo", "plan"),
		},
		{
			"name": "get_job", "title": "Get a job", "annotations": ro,
			"description": "A job's status, step, commits and the last log lines.",
			"inputSchema": obj(map[string]any{"id": str("The job id."),
				"log_lines": map[string]any{"type": "integer", "minimum": 0, "maximum": 400, "description": "How many of the last log lines to include (default 40)."}}, "id"),
		},
		{
			"name": "get_log", "title": "Read a job's log", "annotations": ro,
			"description": "Lines of a job's log, from line from (0-based; negative counts back from the end).",
			"inputSchema": obj(map[string]any{"id": str("The job id."),
				"from":  map[string]any{"type": "integer", "description": "First line (default 0)."},
				"lines": map[string]any{"type": "integer", "minimum": 1, "maximum": 2000, "description": "How many lines (default 200)."}}, "id"),
		},
		{
			"name": "list_jobs", "title": "List jobs", "annotations": ro,
			"description": "Jobs, newest first, with their status.",
			"inputSchema": obj(map[string]any{}),
		},
		{
			"name": "cancel_job", "title": "Cancel a job",
			"description": "Cancel a queued job, or stop a running one (whatever it committed so far is not pushed).",
			"inputSchema": obj(map[string]any{"id": str("The job id.")}, "id"),
		},
	}
}

func textResult(v any, isErr bool) map[string]any {
	b, _ := json.MarshalIndent(v, "", "  ")
	return map[string]any{"content": []map[string]any{{"type": "text", "text": string(b)}}, "isError": isErr}
}

func (m *mcpServer) call(_ context.Context, name string, args json.RawMessage) map[string]any {
	errResult := func(err error) map[string]any { return textResult(map[string]string{"error": err.Error()}, true) }
	switch name {
	case "submit_plan":
		var a struct {
			Repo, Plan, Branch, Title string
			Push                      *bool
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return errResult(err)
		}
		j, err := m.r.Submit(a.Repo, a.Branch, a.Title, a.Plan, a.Push == nil || *a.Push)
		if err != nil {
			return errResult(err)
		}
		return textResult(map[string]any{"id": j.ID, "status": j.Status, "ahead": m.r.Position(j.ID),
			"note": "Poll get_job every minute or two."}, false)
	case "get_job":
		var a struct {
			ID       string `json:"id"`
			LogLines *int   `json:"log_lines"`
		}
		json.Unmarshal(args, &a)
		j, ok := m.r.Get(a.ID)
		if !ok {
			return errResult(fmt.Errorf("no job with id %q", a.ID))
		}
		n := 40
		if a.LogLines != nil {
			n = max(0, min(*a.LogLines, 400))
		}
		lines, total := m.r.Log(a.ID, -n, n)
		return textResult(map[string]any{"job": j, "ahead": m.r.Position(j.ID), "logLines": total, "logTail": lines}, false)
	case "get_log":
		var a struct {
			ID    string `json:"id"`
			From  int    `json:"from"`
			Lines *int   `json:"lines"`
		}
		json.Unmarshal(args, &a)
		if _, ok := m.r.Get(a.ID); !ok {
			return errResult(fmt.Errorf("no job with id %q", a.ID))
		}
		n := 200
		if a.Lines != nil {
			n = max(1, min(*a.Lines, 2000))
		}
		lines, total := m.r.Log(a.ID, a.From, n)
		return textResult(map[string]any{"from": a.From, "total": total, "lines": lines}, false)
	case "list_jobs":
		var out []map[string]any
		for _, j := range m.r.List() {
			out = append(out, map[string]any{"id": j.ID, "title": j.Title, "repo": j.Repo, "status": j.Status, "created": j.Created, "commits": len(j.Commits)})
		}
		if out == nil {
			out = []map[string]any{}
		}
		return textResult(out, false)
	case "cancel_job":
		var a struct {
			ID string `json:"id"`
		}
		json.Unmarshal(args, &a)
		j, err := m.r.Cancel(a.ID)
		if err != nil {
			return errResult(err)
		}
		return textResult(map[string]any{"id": j.ID, "status": j.Status, "note": "A running job stops within a few seconds."}, false)
	}
	return errResult(fmt.Errorf("unknown tool %s", name))
}
