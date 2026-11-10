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
	"strings"
	"time"
)

var mcpVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

const maxWait = 300 // seconds one call may wait for an answer

const mcpInstructions = `Ask Me is where you leave a question for the owner (the user) when you can't get an answer in your own session.

The rule:
1. Ask in your own session first and wait up to 5 minutes for a reply there.
2. Only if nobody answers, call ask_user with the same question. It appears on the owner's "Ask Me" page, which they check from their phone.
3. ask_user waits up to wait_seconds (max 300) for the answer. If it comes back "open", carry on with work that doesn't depend on it, or call get_answer to wait again. Don't ask the same question twice: check list_my_questions first after a restart.
4. If you no longer need the answer, call withdraw_question.

Write the question so it stands on its own: the owner may read it hours later without your session in front of them. Put what you're doing and why you're stuck in context. When the answer is a choice, list the choices in options (they become buttons); the owner can still type something else. Treat the answer as the owner's instruction for that question only.`

type mcpServer struct{ st *store }

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
			"serverInfo":      map[string]any{"name": "ask-me", "title": "Ask Me", "version": "1.0"},
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
	wait := map[string]any{"type": "integer", "minimum": 0, "maximum": maxWait,
		"description": fmt.Sprintf("Seconds to wait for the answer before returning (0 to %d, default 120).", maxWait)}
	obj := func(props map[string]any, req ...string) map[string]any {
		o := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
		if len(req) > 0 {
			o["required"] = req
		}
		return o
	}
	return []map[string]any{
		{
			"name": "ask_user", "title": "Ask the owner",
			"description": "Leave a question on the owner's Ask Me page, after asking in your own session first got no reply within 5 minutes. Waits up to wait_seconds for the answer. Returns the question's id, its status (open, answered, withdrawn) and the answer once there is one.",
			"inputSchema": obj(map[string]any{
				"question":     str("The question, written to make sense on its own."),
				"context":      str("What you're working on and why you need this; a few sentences."),
				"options":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": 8, "description": "Suggested answers, shown as buttons. The owner can still type their own."},
				"from":         str("Who is asking: your session or project name, so the owner knows where it came from."),
				"wait_seconds": wait,
			}, "question"),
		},
		{
			"name": "get_answer", "title": "Get the answer", "annotations": map[string]any{"readOnlyHint": true},
			"description": "Check on a question asked earlier, waiting up to wait_seconds for the answer if it's still open.",
			"inputSchema": obj(map[string]any{"id": str("The id ask_user returned."), "wait_seconds": wait}, "id"),
		},
		{
			"name": "list_my_questions", "title": "List questions", "annotations": map[string]any{"readOnlyHint": true},
			"description": "Questions on the page, newest first (open ones first), optionally only those from one asker. Use it after a restart instead of asking again.",
			"inputSchema": obj(map[string]any{"from": str("Only questions with this 'from'.")}),
		},
		{
			"name": "withdraw_question", "title": "Withdraw a question",
			"description": "Take a question off the page because you no longer need the answer.",
			"inputSchema": obj(map[string]any{"id": str("The question's id.")}, "id"),
		},
	}
}

func textResult(v any, isErr bool) map[string]any {
	b, _ := json.MarshalIndent(v, "", "  ")
	return map[string]any{"content": []map[string]any{{"type": "text", "text": string(b)}}, "isError": isErr}
}

func waitFor(raw *int) time.Duration {
	s := 120
	if raw != nil {
		s = *raw
	}
	s = max(0, min(s, maxWait))
	return time.Duration(s) * time.Second
}

// summary is what an agent gets back about a question.
func summary(q Question) map[string]any {
	out := map[string]any{"id": q.ID, "status": q.Status, "question": q.Question, "asked": q.Asked}
	switch q.Status {
	case "answered":
		out["answer"] = q.Answer
		out["answered"] = q.AnsweredAt
	case "withdrawn":
		out["note"] = "Withdrawn (by you or dismissed by the owner). Don't ask it again unless it still matters."
	default:
		out["note"] = "No answer yet. Do other work that doesn't depend on it, or call get_answer to keep waiting."
	}
	return out
}

func (m *mcpServer) call(ctx context.Context, name string, args json.RawMessage) map[string]any {
	switch name {
	case "ask_user":
		var a struct {
			Question string   `json:"question"`
			Context  string   `json:"context"`
			Options  []string `json:"options"`
			From     string   `json:"from"`
			Wait     *int     `json:"wait_seconds"`
		}
		if json.Unmarshal(args, &a) != nil || strings.TrimSpace(a.Question) == "" {
			return textResult(map[string]string{"error": "question is required"}, true)
		}
		if len(a.Question) > 4000 || len(a.Context) > 8000 {
			return textResult(map[string]string{"error": "keep the question under 4000 characters and the context under 8000"}, true)
		}
		q, err := m.st.Ask(a.From, a.Question, a.Context, a.Options)
		if err != nil {
			return textResult(map[string]string{"error": err.Error()}, true)
		}
		q, _ = m.st.Wait(q.ID, waitFor(a.Wait), ctx.Done())
		return textResult(summary(q), false)
	case "get_answer":
		var a struct {
			ID   string `json:"id"`
			Wait *int   `json:"wait_seconds"`
		}
		json.Unmarshal(args, &a)
		q, ok := m.st.Wait(a.ID, waitFor(a.Wait), ctx.Done())
		if !ok {
			return textResult(map[string]string{"error": "no question with that id"}, true)
		}
		return textResult(summary(q), false)
	case "list_my_questions":
		var a struct {
			From string `json:"from"`
		}
		json.Unmarshal(args, &a)
		var out []map[string]any
		for _, q := range m.st.List() {
			if a.From == "" || strings.EqualFold(q.From, a.From) {
				out = append(out, summary(q))
			}
		}
		if out == nil {
			out = []map[string]any{}
		}
		return textResult(out, false)
	case "withdraw_question":
		var a struct {
			ID string `json:"id"`
		}
		json.Unmarshal(args, &a)
		q, err := m.st.Withdraw(a.ID)
		if err != nil && err != errNotOpen {
			return textResult(map[string]string{"error": err.Error()}, true)
		}
		return textResult(summary(q), false)
	}
	return textResult(map[string]string{"error": "unknown tool " + name}, true)
}
