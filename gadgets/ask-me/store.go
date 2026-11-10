package main

// The questions: kept in memory, saved to one JSON file after every change. An answer wakes any
// agent that is waiting on that question.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const keepAnswered = 200 // answered or withdrawn questions kept for the log

type Question struct {
	ID         string    `json:"id"`
	From       string    `json:"from,omitempty"` // which agent or session asked
	Question   string    `json:"question"`
	Context    string    `json:"context,omitempty"` // what the agent is doing, so the question makes sense
	Options    []string  `json:"options,omitempty"` // suggested answers, shown as buttons
	Asked      time.Time `json:"asked"`
	Status     string    `json:"status"` // open | answered | withdrawn
	Answer     string    `json:"answer,omitempty"`
	AnsweredAt time.Time `json:"answeredAt,omitzero"`
}

var errNotOpen = errors.New("that question isn't open any more")

type store struct {
	mu    sync.Mutex
	path  string
	qs    map[string]*Question
	waits map[string]chan struct{} // closed when the question is answered or withdrawn
}

func openStore(path string) (*store, error) {
	s := &store{path: path, qs: map[string]*Question{}, waits: map[string]chan struct{}{}}
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if len(b) > 0 {
		var list []*Question
		if err := json.Unmarshal(b, &list); err != nil {
			return nil, err
		}
		for _, q := range list {
			s.qs[q.ID] = q
		}
	}
	return s, nil
}

func newID() string {
	b := make([]byte, 5)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// saveLocked writes every question (newest first) atomically. Caller holds mu.
func (s *store) saveLocked() error {
	list := s.listLocked()
	b, _ := json.MarshalIndent(list, "", " ")
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *store) listLocked() []*Question {
	out := make([]*Question, 0, len(s.qs))
	for _, q := range s.qs {
		out = append(out, q)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Asked.After(out[j].Asked) })
	return out
}

// List returns copies, newest first: open questions, then the rest.
func (s *store) List() []Question {
	s.mu.Lock()
	defer s.mu.Unlock()
	var open, done []Question
	for _, q := range s.listLocked() {
		c := *q
		if q.Status == "open" {
			open = append(open, c)
		} else {
			done = append(done, c)
		}
	}
	return append(open, done...)
}

func (s *store) Get(id string) (Question, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q, ok := s.qs[id]
	if !ok {
		return Question{}, false
	}
	return *q, true
}

func (s *store) Ask(from, question, context string, options []string) (Question, error) {
	q := &Question{ID: newID(), From: strings.TrimSpace(from), Question: strings.TrimSpace(question),
		Context: strings.TrimSpace(context), Asked: time.Now().UTC(), Status: "open"}
	for _, o := range options {
		if o = strings.TrimSpace(o); o != "" && len(q.Options) < 8 {
			q.Options = append(q.Options, o)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.qs[q.ID] = q
	return *q, s.saveLocked()
}

// finish answers or withdraws an open question and wakes its waiters.
func (s *store) finish(id, status, answer string) (Question, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q, ok := s.qs[id]
	if !ok {
		return Question{}, errors.New("no such question")
	}
	if q.Status != "open" {
		return *q, errNotOpen
	}
	q.Status, q.Answer, q.AnsweredAt = status, strings.TrimSpace(answer), time.Now().UTC()
	if ch, ok := s.waits[id]; ok {
		close(ch)
		delete(s.waits, id)
	}
	s.pruneLocked()
	return *q, s.saveLocked()
}

func (s *store) Answer(id, answer string) (Question, error) { return s.finish(id, "answered", answer) }
func (s *store) Withdraw(id string) (Question, error)       { return s.finish(id, "withdrawn", "") }

func (s *store) pruneLocked() {
	n := 0
	for _, q := range s.listLocked() {
		if q.Status == "open" {
			continue
		}
		if n++; n > keepAnswered {
			delete(s.qs, q.ID)
		}
	}
}

// Wait blocks until the question is no longer open, the timeout passes or done fires, and
// returns it as it stands.
func (s *store) Wait(id string, timeout time.Duration, done <-chan struct{}) (Question, bool) {
	s.mu.Lock()
	q, ok := s.qs[id]
	if !ok {
		s.mu.Unlock()
		return Question{}, false
	}
	if q.Status != "open" || timeout <= 0 {
		c := *q
		s.mu.Unlock()
		return c, true
	}
	ch, ok := s.waits[id]
	if !ok {
		ch = make(chan struct{})
		s.waits[id] = ch
	}
	s.mu.Unlock()
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case <-ch:
	case <-t.C:
	case <-done:
	}
	return s.Get(id)
}
