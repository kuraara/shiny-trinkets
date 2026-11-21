package main

// The manifest: every file this service wrote, kept in one JSON file. Nothing else is ever
// replaced or removed.

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

type Entry struct {
	Path     string    `json:"path"`  // the subtitle, relative to the media root
	Video    string    `json:"video"` // the video it belongs to
	Label    string    `json:"label"`
	Lang     string    `json:"lang"`
	Size     int       `json:"size"`
	SHA256   string    `json:"sha256"`
	Written  time.Time `json:"written"`
	Replaced bool      `json:"replaced,omitempty"` // the last write replaced an earlier one of ours
}

type manifest struct {
	mu      sync.Mutex
	path    string
	entries map[string]Entry
}

func openManifest(path string) (*manifest, error) {
	m := &manifest{path: path, entries: map[string]Entry{}}
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if len(b) > 0 {
		var list []Entry
		if err := json.Unmarshal(b, &list); err != nil {
			return nil, err
		}
		for _, e := range list {
			m.entries[e.Path] = e
		}
	}
	return m, nil
}

func (m *manifest) saveLocked() error {
	list := m.listLocked("", "")
	b, err := json.MarshalIndent(list, "", " ")
	if err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}

func (m *manifest) listLocked(match, label string) []Entry {
	out := []Entry{}
	match = strings.ToLower(match)
	for _, e := range m.entries {
		if (match == "" || strings.Contains(strings.ToLower(e.Path), match)) && (label == "" || e.Label == label) {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func (m *manifest) List(match, label string) []Entry {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.listLocked(match, label)
}
