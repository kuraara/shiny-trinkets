package main

// Jobs and the worker. A job is a plan for a repository. The worker runs one job at a time:
// clone the repo into a fresh workspace, hand the plan to the executor (an agent CLI, or a plain
// command), collect the commits it made, push them, and keep a log. Everything about a job lives in
// DATA/jobs/<id>/: job.json, plan.md, log.txt and the workspace.

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Config struct {
	Data       string
	Executor   string        // claude | codex | command
	Command    string        // for "command": run with sh -c in the workspace
	TimeLimit  time.Duration // per job
	GitToken   string        // for https://github.com/... clones and pushes
	GitName    string
	GitEmail   string
	AskMeURL   string // optional: the agent may ask the owner through Ask Me (claude only)
	AskMeToken string
	AllowFile  bool // file:// repos (tests)
}

type Job struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Repo     string    `json:"repo"`
	Branch   string    `json:"branch"`
	Push     bool      `json:"push"`
	Executor string    `json:"executor"`
	Status   string    `json:"status"` // queued | running | done | failed | cancelled
	Step     string    `json:"step,omitempty"`
	Created  time.Time `json:"created"`
	Started  time.Time `json:"started,omitzero"`
	Finished time.Time `json:"finished,omitzero"`
	ExitCode *int      `json:"exitCode,omitempty"`
	Commits  []string  `json:"commits,omitempty"`
	Pushed   bool      `json:"pushed"`
	Error    string    `json:"error,omitempty"`
}

type runner struct {
	cfg     Config
	mu      sync.Mutex
	jobs    map[string]*Job
	queue   chan string
	cancels map[string]context.CancelFunc
	userCxl map[string]bool
}

func newRunner(cfg Config) (*runner, error) {
	r := &runner{cfg: cfg, jobs: map[string]*Job{}, queue: make(chan string, 1000),
		cancels: map[string]context.CancelFunc{}, userCxl: map[string]bool{}}
	dirs, _ := filepath.Glob(filepath.Join(cfg.Data, "jobs", "*", "job.json"))
	var requeue []*Job
	for _, f := range dirs {
		var j Job
		if b, err := os.ReadFile(f); err == nil && json.Unmarshal(b, &j) == nil {
			switch j.Status {
			case "running":
				j.Status, j.Error, j.Finished = "failed", "the runner restarted while this job was running", time.Now().UTC()
			case "queued":
				requeue = append(requeue, &j)
			}
			r.jobs[j.ID] = &j
			r.save(&j)
		}
	}
	sort.Slice(requeue, func(a, b int) bool { return requeue[a].Created.Before(requeue[b].Created) })
	for _, j := range requeue {
		r.queue <- j.ID
	}
	return r, nil
}

func (r *runner) dir(id string) string { return filepath.Join(r.cfg.Data, "jobs", id) }

func (r *runner) save(j *Job) {
	b, _ := json.MarshalIndent(j, "", " ")
	os.MkdirAll(r.dir(j.ID), 0o755)
	tmp := filepath.Join(r.dir(j.ID), "job.json.tmp")
	if os.WriteFile(tmp, b, 0o644) == nil {
		os.Rename(tmp, filepath.Join(r.dir(j.ID), "job.json"))
	}
}

func (r *runner) update(id string, f func(j *Job)) Job {
	r.mu.Lock()
	defer r.mu.Unlock()
	j := r.jobs[id]
	f(j)
	r.save(j)
	return *j
}

func (r *runner) Get(id string) (Job, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	j, ok := r.jobs[id]
	if !ok {
		return Job{}, false
	}
	return *j, true
}

func (r *runner) List() []Job {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Job, 0, len(r.jobs))
	for _, j := range r.jobs {
		out = append(out, *j)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Created.After(out[b].Created) })
	return out
}

// Position is how many jobs are ahead of this one (0 = running or next).
func (r *runner) Position(id string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	me, ok := r.jobs[id]
	if !ok || me.Status != "queued" {
		return 0
	}
	n := 0
	for _, j := range r.jobs {
		if j.Status == "running" || (j.Status == "queued" && j.Created.Before(me.Created)) {
			n++
		}
	}
	return n
}

func newID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return time.Now().UTC().Format("0102-1504") + "-" + hex.EncodeToString(b)
}

var errBadRepo = errors.New("repo must be an https:// git URL")

func (r *runner) Submit(repo, branch, title, plan string, push bool) (Job, error) {
	repo, branch, plan = strings.TrimSpace(repo), strings.TrimSpace(branch), strings.TrimSpace(plan)
	if !(strings.HasPrefix(repo, "https://") || (r.cfg.AllowFile && strings.HasPrefix(repo, "file://"))) {
		return Job{}, errBadRepo
	}
	if plan == "" {
		return Job{}, errors.New("the plan is empty")
	}
	if strings.ContainsAny(branch, " ~^:?*[\\") || strings.HasPrefix(branch, "-") {
		return Job{}, errors.New("that isn't a valid branch name")
	}
	if title = strings.TrimSpace(title); title == "" {
		title = firstLine(plan)
	}
	j := &Job{ID: newID(), Title: title, Repo: repo, Branch: branch, Push: push, Executor: r.cfg.Executor,
		Status: "queued", Created: time.Now().UTC()}
	if err := os.MkdirAll(r.dir(j.ID), 0o755); err != nil {
		return Job{}, err
	}
	if err := os.WriteFile(filepath.Join(r.dir(j.ID), "plan.md"), []byte(plan+"\n"), 0o644); err != nil {
		return Job{}, err
	}
	r.mu.Lock()
	r.jobs[j.ID] = j
	r.save(j)
	r.mu.Unlock()
	r.queue <- j.ID
	return *j, nil
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(strings.TrimLeft(l, "# ")); l != "" {
			if len(l) > 80 {
				l = l[:80] + "…"
			}
			return l
		}
	}
	return "Plan"
}

func (r *runner) Cancel(id string) (Job, error) {
	r.mu.Lock()
	j, ok := r.jobs[id]
	if !ok {
		r.mu.Unlock()
		return Job{}, errors.New("no job with that id")
	}
	switch j.Status {
	case "queued":
		j.Status, j.Finished = "cancelled", time.Now().UTC()
		r.save(j)
	case "running":
		r.userCxl[id] = true
		if c := r.cancels[id]; c != nil {
			c()
		}
	}
	out := *j
	r.mu.Unlock()
	return out, nil
}

// Log returns lines [from, from+n) of a job's log and the total line count.
func (r *runner) Log(id string, from, n int) ([]string, int) {
	b, err := os.ReadFile(filepath.Join(r.dir(id), "log.txt"))
	if err != nil {
		return []string{}, 0
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(b) == 0 {
		lines = nil
	}
	total := len(lines)
	if from < 0 { // negative: the last -from lines
		from = max(0, total+from)
	}
	from = min(from, total)
	end := min(total, from+n)
	return lines[from:end], total
}

// Work runs queued jobs one at a time until ctx ends.
func (r *runner) Work(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-r.queue:
			if j, ok := r.Get(id); ok && j.Status == "queued" {
				r.run(ctx, id)
			}
		}
	}
}

// redactor hides secrets in anything written to the log.
type redactor struct {
	w       io.Writer
	secrets []string
}

func (d redactor) Write(p []byte) (int, error) {
	s := string(p)
	for _, x := range d.secrets {
		if x != "" {
			s = strings.ReplaceAll(s, x, "***")
		}
	}
	_, err := io.WriteString(d.w, s)
	return len(p), err
}

func (r *runner) run(parent context.Context, id string) {
	j := r.update(id, func(j *Job) { j.Status, j.Started, j.Step = "running", time.Now().UTC(), "clone" })
	ctx, cancel := context.WithTimeout(parent, r.cfg.TimeLimit)
	defer cancel()
	r.mu.Lock()
	r.cancels[id] = cancel
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.cancels, id)
		delete(r.userCxl, id)
		r.mu.Unlock()
	}()

	f, err := os.OpenFile(filepath.Join(r.dir(id), "log.txt"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		r.update(id, func(j *Job) { j.Status, j.Error, j.Finished = "failed", err.Error(), time.Now().UTC() })
		return
	}
	defer f.Close()
	log := redactor{w: f, secrets: []string{r.cfg.GitToken, r.cfg.AskMeToken}}
	say := func(format string, a ...any) {
		fmt.Fprintf(log, "[%s] "+format+"\n", append([]any{time.Now().Format("15:04:05")}, a...)...)
	}
	ws := filepath.Join(r.dir(id), "workspace")
	git := func(args ...string) (string, error) {
		c := exec.CommandContext(ctx, "git", args...)
		c.Dir = ws
		c.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		out, err := c.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	fail := func(step string, err error, out string) {
		if out != "" {
			fmt.Fprintln(log, out)
		}
		byUser := r.cancelledByUser(id)
		if byUser {
			say("cancelled")
		} else {
			say("%s failed: %v", step, err)
		}
		r.update(id, func(j *Job) {
			j.Status, j.Error, j.Finished = "failed", step+" failed: "+err.Error(), time.Now().UTC()
			if byUser {
				j.Status, j.Error = "cancelled", ""
			} else if ctx.Err() == context.DeadlineExceeded {
				j.Error = fmt.Sprintf("stopped at the time limit (%s) during %s", r.cfg.TimeLimit, step)
			}
		})
	}

	// 1. clone
	say("job %s: %s", id, j.Title)
	say("cloning %s%s", j.Repo, map[bool]string{true: " (branch " + j.Branch + ")", false: ""}[j.Branch != ""])
	os.RemoveAll(ws)
	args := []string{"clone", "--single-branch"}
	if j.Branch != "" {
		args = append(args, "--branch", j.Branch)
	}
	c := exec.CommandContext(ctx, "git", append(args, r.authURL(j.Repo), ws)...)
	c.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := c.CombinedOutput(); err != nil {
		fail("clone", err, string(out))
		return
	}
	branch, _ := git("rev-parse", "--abbrev-ref", "HEAD")
	base, _ := git("rev-parse", "HEAD")
	git("config", "user.name", r.cfg.GitName)
	git("config", "user.email", r.cfg.GitEmail)
	r.update(id, func(j *Job) { j.Branch, j.Step = branch, "run" })
	say("on %s at %s; running the %s executor (time limit %s)", branch, short(base), r.cfg.Executor, r.cfg.TimeLimit)

	// 2. the executor
	cmd, err := r.executor(ctx, id, ws, branch)
	if err != nil {
		fail("run", err, "")
		return
	}
	cmd.Stdout, cmd.Stderr = log, log
	err = cmd.Run()
	code := -1
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	r.update(id, func(j *Job) { j.ExitCode = &code })
	if err != nil {
		say("the executor exited with %d", code)
	} else {
		say("the executor finished")
	}

	// 3. what it committed
	commits, _ := git("log", "--format=%h %s", base+"..HEAD")
	var list []string
	if commits != "" {
		list = strings.Split(commits, "\n")
	}
	if dirty, _ := git("status", "--porcelain"); dirty != "" {
		say("left uncommitted changes (not pushed):\n%s", dirty)
	}
	r.update(id, func(j *Job) { j.Commits = list })
	say("%d new commit(s)", len(list))
	if err != nil {
		fail("run", err, "")
		return
	}

	// 4. push
	pushed := false
	if j.Push && len(list) > 0 {
		r.update(id, func(j *Job) { j.Step = "push" })
		if out, err := git("push", r.authURL(j.Repo), "HEAD:refs/heads/"+branch); err != nil {
			fail("push", err, out)
			return
		}
		pushed = true
		say("pushed to %s", branch)
	}
	r.update(id, func(j *Job) { j.Status, j.Pushed, j.Step, j.Finished = "done", pushed, "", time.Now().UTC() })
	say("done")
}

func (r *runner) cancelledByUser(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.userCxl[id]
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// authURL adds the token to github.com https URLs.
func (r *runner) authURL(repo string) string {
	if r.cfg.GitToken != "" && strings.HasPrefix(repo, "https://github.com/") {
		return "https://x-access-token:" + r.cfg.GitToken + "@" + strings.TrimPrefix(repo, "https://")
	}
	return repo
}

const promptText = `You are running unattended on a build server; nobody is watching this session.
Carry out the plan in %s in this repository (your working directory), on branch %s.

- Read the repository's own instructions first (CLAUDE.md, AGENTS.md, README) and follow them.
- Work through the plan phase by phase. Build and run the tests as you go, and fix what fails.
- Commit after each phase with a clear message. Don't push: the runner pushes when you finish.
- %s
- Finish with a short summary: what you did, how you tested it, and what's left.`

const askNone = "If you're blocked on a decision only the owner can make, don't guess: stop, and put the question at the top of your summary."
const askMe = "If you're blocked on a decision only the owner can make, ask with the ask-me tool ask_user (context, options, from: \"plan-runner job %s\", wait_seconds 300); call get_answer to keep waiting, up to 30 minutes in total. Do other parts of the plan meanwhile. If there's still no answer, stop and put the question at the top of your summary."

// executor builds the command that carries out the plan.
func (r *runner) executor(ctx context.Context, id, ws, branch string) (*exec.Cmd, error) {
	planPath := filepath.Join(r.dir(id), "plan.md")
	ask := askNone
	if r.cfg.AskMeURL != "" && r.cfg.Executor == "claude" {
		ask = fmt.Sprintf(askMe, id)
	}
	prompt := fmt.Sprintf(promptText, planPath, branch, ask)
	var cmd *exec.Cmd
	switch r.cfg.Executor {
	case "claude":
		args := []string{"-p", prompt, "--permission-mode", "bypassPermissions", "--add-dir", r.dir(id)}
		if r.cfg.AskMeURL != "" {
			mcp, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"ask-me": map[string]any{
				"type": "http", "url": r.cfg.AskMeURL, "headers": map[string]string{"Authorization": "Bearer " + r.cfg.AskMeToken}}}})
			args = append(args, "--mcp-config", string(mcp))
		}
		cmd = exec.CommandContext(ctx, "claude", args...)
	case "codex":
		cmd = exec.CommandContext(ctx, "codex", "exec", "--dangerously-bypass-approvals-and-sandbox", prompt)
	case "command":
		if r.cfg.Command == "" {
			return nil, errors.New("EXECUTOR=command needs EXECUTOR_COMMAND")
		}
		cmd = exec.CommandContext(ctx, "sh", "-c", r.cfg.Command)
	default:
		return nil, fmt.Errorf("unknown executor %q (claude, codex or command)", r.cfg.Executor)
	}
	cmd.Dir = ws
	cmd.Env = append(childEnv(), "PLAN_FILE="+planPath, "WORKSPACE="+ws, "JOB_ID="+id, "BRANCH="+branch, "PROMPT="+prompt)
	// the whole process group goes when the job is cancelled or times out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	return cmd, nil
}

// childEnv is the runner's environment without its own secrets: the agent gets its model
// credentials (ANTHROPIC/CLAUDE/OPENAI variables) but not the runner's token or the git token.
func childEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "TOKEN", "GIT_TOKEN", "GH_TOKEN", "ASKME_TOKEN":
			continue
		}
		out = append(out, kv)
	}
	return out
}

// tail reads the last n lines of a file (for summaries).
func tail(path string, n int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var lines []string
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 1<<20), 1<<20)
	for s.Scan() {
		lines = append(lines, s.Text())
		if len(lines) > n {
			lines = lines[1:]
		}
	}
	return lines
}
