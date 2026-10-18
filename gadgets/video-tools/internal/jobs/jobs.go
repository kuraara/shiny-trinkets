// Package jobs runs long video work in the background: a bounded worker queue, per-job folders,
// progress, cancellation, crash recovery and a janitor that deletes old work.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kuraara/clowd-docker-stuff/video-tools/internal/storage"
)

type Status string

const (
	Queued    Status = "queued"
	Running   Status = "running"
	Review    Status = "review" // panel transcript analysed; waiting for speaker names
	Done      Status = "done"
	Failed    Status = "failed"
	Cancelled Status = "cancelled"
)

// Active reports whether the job still needs its inputs and worker.
func (s Status) Active() bool { return s == Queued || s == Running }

type Output struct {
	Name  string `json:"name"`  // file name inside the job's out/ folder
	Label string `json:"label"` // what to call it in the UI
	Kind  string `json:"kind"`  // image | video | text | json
	Size  int64  `json:"size"`
}

type Input struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

type Job struct {
	ID       string          `json:"id"`
	Kind     string          `json:"kind"` // panel | join
	Title    string          `json:"title"`
	Status   Status          `json:"status"`
	Progress int             `json:"progress"`
	Message  string          `json:"message"`
	Error    string          `json:"error,omitempty"`
	Created  time.Time       `json:"created"`
	Updated  time.Time       `json:"updated"`
	Inputs   []Input         `json:"inputs"`
	Params   json.RawMessage `json:"params,omitempty"`
	Outputs  []Output        `json:"outputs,omitempty"`
	Notes    []string        `json:"notes,omitempty"` // summary lines worth showing (mode used, warnings)
	Log      []string        `json:"log,omitempty"`   // tail of tool output, for diagnosing failures
}

// Runner does the work for one kind of job. Returning ErrRequeue puts the job back in the queue.
type Runner func(ctx context.Context, h *Handle) error

// ErrRequeue asks the manager to retry the job shortly (e.g. models still downloading).
var ErrRequeue = errors.New("requeue")

type Manager struct {
	dir     string
	store   *storage.Store
	ttl     time.Duration
	mu      sync.Mutex
	jobs    map[string]*Job
	cancels map[string]context.CancelFunc
	runners map[string]Runner
	queue   chan string
	saved   map[string]time.Time
	running sync.WaitGroup
	stop    atomic.Bool
}

func NewManager(store *storage.Store, ttl time.Duration) *Manager {
	m := &Manager{dir: store.JobsDir, store: store, ttl: ttl, jobs: map[string]*Job{},
		cancels: map[string]context.CancelFunc{}, runners: map[string]Runner{},
		queue: make(chan string, 1000), saved: map[string]time.Time{}}
	m.load()
	return m
}

func (m *Manager) Register(kind string, r Runner) { m.runners[kind] = r }

// load restores jobs from disk. Anything that was mid-flight when the container stopped is failed,
// and its scratch folder removed, rather than silently resumed.
func (m *Manager) load() {
	ents, _ := os.ReadDir(m.dir)
	for _, e := range ents {
		b, err := os.ReadFile(filepath.Join(m.dir, e.Name(), "job.json"))
		if err != nil {
			continue
		}
		var j Job
		if json.Unmarshal(b, &j) != nil || j.ID != e.Name() {
			continue
		}
		if j.Status.Active() {
			j.Status, j.Error = Failed, "interrupted by a restart — please run it again"
			_ = os.RemoveAll(filepath.Join(m.dir, j.ID, "work"))
		}
		m.jobs[j.ID] = &j
		m.saveLocked(&j, true)
	}
}

func (m *Manager) JobDir(id string) string  { return filepath.Join(m.dir, id) }
func (m *Manager) OutDir(id string) string  { return filepath.Join(m.dir, id, "out") }
func (m *Manager) WorkDir(id string) string { return filepath.Join(m.dir, id, "work") }

// Submit creates a job and queues it.
func (m *Manager) Submit(kind, title string, inputs []storage.File, params any) (Job, error) {
	if _, ok := m.runners[kind]; !ok {
		return Job{}, fmt.Errorf("unknown job kind %q", kind)
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return Job{}, err
	}
	now := time.Now()
	j := &Job{ID: storage.NewID("j_"), Kind: kind, Title: title, Status: Queued, Message: "waiting in queue",
		Created: now, Updated: now, Params: raw}
	for _, f := range inputs {
		j.Inputs = append(j.Inputs, Input{ID: f.ID, Name: f.Name, Size: f.Size})
	}
	for _, d := range []string{m.OutDir(j.ID), m.WorkDir(j.ID)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return Job{}, err
		}
	}
	m.mu.Lock()
	m.jobs[j.ID] = j
	m.saveLocked(j, true)
	m.mu.Unlock()
	select {
	case m.queue <- j.ID:
	default:
		m.Update(j.ID, func(j *Job) { j.Status, j.Error = Failed, "queue is full" })
		return *j, errors.New("queue is full")
	}
	return m.Get(j.ID)
}

func (m *Manager) Get(id string) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return Job{}, fmt.Errorf("job %q not found", id)
	}
	return clone(j), nil
}

func (m *Manager) List() []Job {
	m.mu.Lock()
	out := make([]Job, 0, len(m.jobs))
	for _, j := range m.jobs {
		out = append(out, clone(j))
	}
	m.mu.Unlock()
	sort.Slice(out, func(a, b int) bool { return out[a].Created.After(out[b].Created) })
	return out
}

func clone(j *Job) Job {
	c := *j
	c.Inputs = append([]Input(nil), j.Inputs...)
	c.Outputs = append([]Output(nil), j.Outputs...)
	c.Notes = append([]string(nil), j.Notes...)
	c.Log = append([]string(nil), j.Log...)
	return c
}

// Update applies fn to a job under the lock and persists it.
func (m *Manager) Update(id string, fn func(*Job)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return fmt.Errorf("job %q not found", id)
	}
	fn(j)
	j.Updated = time.Now()
	m.saveLocked(j, true)
	return nil
}

// saveLocked writes job.json atomically. Progress-only updates are throttled to one write per 2 s.
func (m *Manager) saveLocked(j *Job, force bool) {
	if !force && time.Since(m.saved[j.ID]) < 2*time.Second {
		return
	}
	m.saved[j.ID] = time.Now()
	b, _ := json.MarshalIndent(j, "", " ")
	p := filepath.Join(m.dir, j.ID, "job.json")
	if err := os.WriteFile(p+".tmp", b, 0o644); err == nil {
		_ = os.Rename(p+".tmp", p)
	}
}

// Cancel stops a queued or running job.
func (m *Manager) Cancel(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return fmt.Errorf("job %q not found", id)
	}
	if !j.Status.Active() {
		return nil
	}
	if c := m.cancels[id]; c != nil {
		c()
	}
	j.Status, j.Message, j.Updated = Cancelled, "cancelled", time.Now()
	m.saveLocked(j, true)
	return nil
}

// Delete removes a job and all its files (cancelling it first if needed).
func (m *Manager) Delete(id string) error {
	_ = m.Cancel(id)
	m.mu.Lock()
	_, ok := m.jobs[id]
	delete(m.jobs, id)
	delete(m.saved, id)
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("job %q not found", id)
	}
	// a cancelled ffmpeg may still be exiting; give it a moment before removing its files
	go func() { time.Sleep(3 * time.Second); _ = os.RemoveAll(m.JobDir(id)) }()
	return nil
}

// InputInUse reports whether an active job still needs this input file.
func (m *Manager) InputInUse(fileID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		if j.Status.Active() {
			for _, in := range j.Inputs {
				if in.ID == fileID {
					return true
				}
			}
		}
	}
	return false
}

// Start launches the workers. Jobs run strictly `workers` at a time; the rest wait in the queue.
func (m *Manager) Start(ctx context.Context, workers int) {
	for i := 0; i < workers; i++ {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case id := <-m.queue:
					m.run(ctx, id)
				}
			}
		}()
	}
	go m.janitor(ctx)
}

func (m *Manager) run(parent context.Context, id string) {
	m.mu.Lock()
	j, ok := m.jobs[id]
	if !ok || j.Status != Queued || m.stop.Load() {
		m.mu.Unlock()
		return
	}
	m.running.Add(1) // under the lock, so Shutdown can't miss a job that is starting
	defer m.running.Done()
	ctx, cancel := context.WithCancel(parent)
	m.cancels[id] = cancel
	j.Status, j.Message, j.Error, j.Updated = Running, "starting", "", time.Now()
	m.saveLocked(j, true)
	runner := m.runners[j.Kind]
	m.mu.Unlock()

	h := &Handle{m: m, id: id, final: Done}
	err := func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("internal error: %v", r)
			}
		}()
		return runner(ctx, h)
	}()
	cancel()

	m.mu.Lock()
	delete(m.cancels, id)
	j, ok = m.jobs[id]
	if !ok { // deleted while running
		m.mu.Unlock()
		return
	}
	switch {
	case errors.Is(err, ErrRequeue):
		j.Status = Queued
		m.saveLocked(j, true)
		m.mu.Unlock()
		go func() { time.Sleep(10 * time.Second); m.queue <- id }()
		return
	case m.stop.Load() && err != nil:
		j.Status, j.Error, j.Message = Failed, "interrupted by a restart — please run it again", "failed"
	case j.Status == Cancelled || ctx.Err() != nil && errors.Is(err, context.Canceled):
		j.Status, j.Message = Cancelled, "cancelled"
	case err != nil:
		j.Status, j.Error, j.Message = Failed, err.Error(), "failed"
		log.Printf("job %s (%s) failed: %v", id, j.Kind, err)
	default:
		j.Status, j.Progress = h.final, 100
		if h.final == Done {
			j.Message = "finished"
		}
	}
	j.Updated = time.Now()
	m.saveLocked(j, true)
	m.mu.Unlock()
	// scratch space never outlives the run, whatever happened
	_ = os.RemoveAll(m.WorkDir(id))
	_ = os.MkdirAll(m.WorkDir(id), 0o755)
}

// Shutdown stops every running job (killing its ffmpeg/worker processes) and waits up to timeout
// for them to exit, so nothing is left running after the server stops.
func (m *Manager) Shutdown(timeout time.Duration) {
	m.mu.Lock()
	m.stop.Store(true)
	for _, c := range m.cancels {
		c()
	}
	m.mu.Unlock()
	done := make(chan struct{})
	go func() { m.running.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(timeout):
		log.Printf("shutdown: jobs still stopping after %s", timeout)
	}
}

// janitor deletes finished jobs and unused uploads older than the TTL, every 10 minutes.
func (m *Manager) janitor(ctx context.Context) {
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for {
		m.Sweep()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Sweep runs one cleanup pass now.
func (m *Manager) Sweep() {
	var old []string
	m.mu.Lock()
	for id, j := range m.jobs {
		if !j.Status.Active() && time.Since(j.Updated) > m.ttl {
			old = append(old, id)
		}
	}
	m.mu.Unlock()
	for _, id := range old {
		_ = m.Delete(id)
	}
	n := m.store.SweepUploads(m.ttl, m.InputInUse)
	if len(old)+n > 0 {
		log.Printf("janitor: removed %d old jobs and %d unused uploads", len(old), n)
	}
}

// Handle is what a runner uses to report back.
type Handle struct {
	m     *Manager
	id    string
	final Status
}

func (h *Handle) ID() string      { return h.id }
func (h *Handle) OutDir() string  { return h.m.OutDir(h.id) }
func (h *Handle) WorkDir() string { return h.m.WorkDir(h.id) }
func (h *Handle) Job() Job        { j, _ := h.m.Get(h.id); return j }

// Params decodes the job's parameters.
func (h *Handle) Params(v any) error {
	j := h.Job()
	return json.Unmarshal(j.Params, v)
}

// Progress records percentage and a short status message (throttled persistence).
func (h *Handle) Progress(pct int, msg string) {
	h.m.mu.Lock()
	defer h.m.mu.Unlock()
	if j, ok := h.m.jobs[h.id]; ok && j.Status == Running {
		j.Progress, j.Message, j.Updated = min(max(pct, 0), 99), msg, time.Now()
		h.m.saveLocked(j, false)
	}
}

// Log appends a line to the job's log tail (last 60 lines kept).
func (h *Handle) Log(line string) {
	h.m.mu.Lock()
	defer h.m.mu.Unlock()
	if j, ok := h.m.jobs[h.id]; ok {
		j.Log = append(j.Log, line)
		if len(j.Log) > 60 {
			j.Log = j.Log[len(j.Log)-60:]
		}
	}
}

// Note adds a summary line shown with the result.
func (h *Handle) Note(s string) { _ = h.m.Update(h.id, func(j *Job) { j.Notes = append(j.Notes, s) }) }

// AddOutput registers a finished file in out/.
func (h *Handle) AddOutput(name, label, kind string) {
	var size int64
	if st, err := os.Stat(filepath.Join(h.OutDir(), name)); err == nil {
		size = st.Size()
	}
	_ = h.m.Update(h.id, func(j *Job) {
		for i, o := range j.Outputs {
			if o.Name == name {
				j.Outputs[i] = Output{name, label, kind, size}
				return
			}
		}
		j.Outputs = append(j.Outputs, Output{name, label, kind, size})
	})
}

// FinishAs sets the status the job ends in when the runner returns nil (default Done).
func (h *Handle) FinishAs(s Status, msg string) {
	h.final = s
	_ = h.m.Update(h.id, func(j *Job) { j.Message = msg })
}

// Store gives runners access to inputs.
func (h *Handle) Store() *storage.Store { return h.m.store }

// InputInUseElsewhere reports whether another active job also needs the file.
func (h *Handle) InputInUseElsewhere(fileID string) bool {
	h.m.mu.Lock()
	defer h.m.mu.Unlock()
	for id, j := range h.m.jobs {
		if id != h.id && j.Status.Active() {
			for _, in := range j.Inputs {
				if in.ID == fileID {
					return true
				}
			}
		}
	}
	return false
}
