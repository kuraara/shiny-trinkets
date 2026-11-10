package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newServer(t *testing.T) (*httptest.Server, *store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "questions.json")
	st, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(routes(st))
	t.Cleanup(srv.Close)
	return srv, st, path
}

// mcpCall calls a tool and returns the parsed JSON text of its result.
func mcpCall(t *testing.T, base, tool string, args map[string]any) (map[string]any, bool) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": args}})
	r, err := http.Post(base+"/mcp", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var resp struct {
		Result struct {
			Content []struct{ Text string } `json:"content"`
			IsError bool                    `json:"isError"`
		} `json:"result"`
	}
	json.NewDecoder(r.Body).Decode(&resp)
	var out map[string]any
	json.Unmarshal([]byte(resp.Result.Content[0].Text), &out)
	return out, resp.Result.IsError
}

func answer(t *testing.T, base, id, text string) int {
	b, _ := json.Marshal(map[string]string{"answer": text})
	r, err := http.Post(base+"/api/questions/"+id+"/answer", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	return r.StatusCode
}

func TestAskThenAnswerWhileWaiting(t *testing.T) {
	srv, st, path := newServer(t)
	done := make(chan map[string]any)
	go func() {
		out, _ := mcpCall(t, srv.URL, "ask_user", map[string]any{"question": "Deploy now?", "options": []string{"Yes", "No"},
			"from": "test", "context": "testing", "wait_seconds": 10})
		done <- out
	}()
	// wait for the question to appear, then answer it on the page's API
	var id string
	for i := 0; i < 100 && id == ""; i++ {
		if l := st.List(); len(l) > 0 {
			id = l[0].ID
		}
		time.Sleep(10 * time.Millisecond)
	}
	if id == "" {
		t.Fatal("question never appeared")
	}
	if got := st.List()[0].Options; len(got) != 2 {
		t.Errorf("options: %v", got)
	}
	if c := answer(t, srv.URL, id, "Yes, go"); c != 200 {
		t.Fatalf("answer: %d", c)
	}
	select {
	case out := <-done:
		if out["status"] != "answered" || out["answer"] != "Yes, go" {
			t.Errorf("waiting ask_user got %v", out)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ask_user didn't return after the answer")
	}
	if c := answer(t, srv.URL, id, "again"); c != http.StatusConflict {
		t.Errorf("second answer: %d, want 409", c)
	}
	// saved: a fresh store reads it back
	st2, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if q, ok := st2.Get(id); !ok || q.Answer != "Yes, go" {
		t.Errorf("not saved: %+v", q)
	}
}

func TestOpenThenGetAnswerAndWithdraw(t *testing.T) {
	srv, _, _ := newServer(t)
	out, isErr := mcpCall(t, srv.URL, "ask_user", map[string]any{"question": "Which port?", "wait_seconds": 0})
	if isErr || out["status"] != "open" {
		t.Fatalf("ask: %v", out)
	}
	id := out["id"].(string)
	out, _ = mcpCall(t, srv.URL, "get_answer", map[string]any{"id": id, "wait_seconds": 0})
	if out["status"] != "open" {
		t.Errorf("get_answer: %v", out)
	}
	out, _ = mcpCall(t, srv.URL, "withdraw_question", map[string]any{"id": id})
	if out["status"] != "withdrawn" {
		t.Errorf("withdraw: %v", out)
	}
	if c := answer(t, srv.URL, id, "8108"); c != http.StatusConflict {
		t.Errorf("answering a withdrawn question: %d, want 409", c)
	}
	if _, isErr := mcpCall(t, srv.URL, "ask_user", map[string]any{"question": "  "}); !isErr {
		t.Error("empty question accepted")
	}
}

func TestPageAndList(t *testing.T) {
	srv, _, _ := newServer(t)
	mcpCall(t, srv.URL, "ask_user", map[string]any{"question": "First?", "wait_seconds": 0})
	r, err := http.Get(srv.URL + "/api/questions")
	if err != nil {
		t.Fatal(err)
	}
	var body struct{ Questions []Question }
	json.NewDecoder(r.Body).Decode(&body)
	r.Body.Close()
	if len(body.Questions) != 1 || body.Questions[0].Question != "First?" {
		t.Errorf("list: %+v", body.Questions)
	}
	r, _ = http.Get(srv.URL + "/")
	if r.StatusCode != 200 {
		t.Errorf("page: %d", r.StatusCode)
	}
	b := new(bytes.Buffer)
	b.ReadFrom(r.Body)
	if !strings.Contains(b.String(), "Ask Me") {
		t.Error("page title missing")
	}
}
