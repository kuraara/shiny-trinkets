package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// bareRepo makes a bare git repo with one commit on main and returns its file:// URL.
func bareRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	bare, work := filepath.Join(dir, "origin.git"), filepath.Join(dir, "seed")
	run := func(d string, args ...string) {
		c := exec.Command("git", args...)
		c.Dir = d
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(dir, "init", "-q", "--bare", "-b", "main", bare)
	run(dir, "init", "-q", "-b", "main", work)
	os.WriteFile(filepath.Join(work, "README.md"), []byte("hello\n"), 0o644)
	run(work, "add", ".")
	run(work, "commit", "-qm", "first")
	run(work, "push", "-q", bare, "main")
	return "file://" + bare, bare
}

func newTestRunner(t *testing.T, command string, limit time.Duration) *runner {
	t.Helper()
	r, err := newRunner(Config{Data: t.TempDir(), Executor: "command", Command: command, TimeLimit: limit,
		GitName: "Plan Runner", GitEmail: "pr@test", AllowFile: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go r.Work(ctx)
	return r
}

func waitDone(t *testing.T, r *runner, id string, within time.Duration) Job {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if j, _ := r.Get(id); j.Status != "queued" && j.Status != "running" {
			return j
		}
		time.Sleep(50 * time.Millisecond)
	}
	j, _ := r.Get(id)
	t.Fatalf("job still %s after %s", j.Status, within)
	return j
}

func TestJobCommitsAndPushes(t *testing.T) {
	url, bare := bareRepo(t)
	// the "agent": reads the plan, makes two commits
	r := newTestRunner(t, `grep -q "Add notes" "$PLAN_FILE" && echo one > notes.txt && git add notes.txt && git commit -qm "phase 1: notes" && echo two >> notes.txt && git commit -qam "phase 2: more notes" && echo agent finished`, time.Minute)
	j, err := r.Submit(url, "", "", "# Add notes\n\nWrite notes.txt in two phases.", true)
	if err != nil {
		t.Fatal(err)
	}
	if j.Title != "Add notes" {
		t.Errorf("title from the plan: %q", j.Title)
	}
	j = waitDone(t, r, j.ID, 30*time.Second)
	if j.Status != "done" || len(j.Commits) != 2 || !j.Pushed || j.Branch != "main" || j.ExitCode == nil || *j.ExitCode != 0 {
		t.Fatalf("job: %+v", j)
	}
	out, _ := exec.Command("git", "--git-dir", bare, "log", "--format=%s", "main").Output()
	if !strings.HasPrefix(string(out), "phase 2: more notes\nphase 1: notes\nfirst") {
		t.Errorf("origin after push:\n%s", out)
	}
	lines, _ := r.Log(j.ID, 0, 1000)
	if all := strings.Join(lines, "\n"); !strings.Contains(all, "agent finished") || !strings.Contains(all, "2 new commit(s)") {
		t.Errorf("log:\n%s", all)
	}
}

func TestFailedJobDoesNotPush(t *testing.T) {
	url, bare := bareRepo(t)
	r := newTestRunner(t, `echo x > x.txt && git add x.txt && git commit -qm half && exit 3`, time.Minute)
	j, _ := r.Submit(url, "main", "fails", "plan", true)
	j = waitDone(t, r, j.ID, 30*time.Second)
	if j.Status != "failed" || j.ExitCode == nil || *j.ExitCode != 3 || j.Pushed || len(j.Commits) != 1 {
		t.Fatalf("job: %+v", j)
	}
	out, _ := exec.Command("git", "--git-dir", bare, "log", "--format=%s", "main").Output()
	if strings.TrimSpace(string(out)) != "first" {
		t.Errorf("a failed job pushed:\n%s", out)
	}
}

func TestCancelAndTimeLimit(t *testing.T) {
	url, _ := bareRepo(t)
	r := newTestRunner(t, `sleep 30`, 2*time.Second)
	a, _ := r.Submit(url, "", "slow", "plan", true)
	b, _ := r.Submit(url, "", "queued", "plan", true)
	if p := r.Position(b.ID); p != 1 {
		t.Errorf("position of the second job: %d", p)
	}
	time.Sleep(500 * time.Millisecond)
	r.Cancel(a.ID)
	if j := waitDone(t, r, a.ID, 10*time.Second); j.Status != "cancelled" {
		t.Errorf("cancelled job: %+v", j)
	}
	// the second one runs into the 2 s limit
	if j := waitDone(t, r, b.ID, 15*time.Second); j.Status != "failed" || !strings.Contains(j.Error, "time limit") {
		t.Errorf("timed-out job: %+v", j)
	}
}

func TestSubmitChecksAndMCP(t *testing.T) {
	r := newTestRunner(t, "true", time.Minute)
	r.cfg.AllowFile = false
	if _, err := r.Submit("file:///etc", "", "", "plan", true); err == nil {
		t.Error("file:// accepted")
	}
	if _, err := r.Submit("https://example.com/x.git", "-x", "", "plan", true); err == nil {
		t.Error("bad branch accepted")
	}
	if _, err := r.Submit("https://example.com/x.git", "", "", "  ", true); err == nil {
		t.Error("empty plan accepted")
	}
	srv := httptest.NewServer(withAuth("tok", routes(r)))
	defer srv.Close()
	call := func(auth bool, body map[string]any) (int, map[string]any) {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", srv.URL+"/mcp", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		if auth {
			req.Header.Set("Authorization", "Bearer tok")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	if c, _ := call(false, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}); c != 401 {
		t.Errorf("mcp without token: %d", c)
	}
	_, out := call(true, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	tools := out["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 5 {
		t.Errorf("tools: %d", len(tools))
	}
	_, out = call(true, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "submit_plan", "arguments": map[string]any{"repo": "file:///x", "plan": "p"}}})
	if out["result"].(map[string]any)["isError"] != true {
		t.Errorf("submit with a file:// repo over MCP: %v", out)
	}
}
