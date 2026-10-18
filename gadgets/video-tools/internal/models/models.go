// Package models tracks the speech models the panel transcript needs and downloads them in the
// background on first start, so the web UI is usable immediately.
package models

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

type Models struct {
	Dir    string
	Script string
	mu     sync.Mutex
	state  string // missing | downloading | ready | failed
	detail string
}

// Required files, relative to Dir.
var required = []string{
	"sherpa-onnx-whisper-small.en/small.en-encoder.int8.onnx",
	"sherpa-onnx-whisper-small.en/small.en-decoder.int8.onnx",
	"sherpa-onnx-whisper-small.en/small.en-tokens.txt",
	"vosk-model-small-en-us-0.15/am/final.mdl",
}

func New(dir, script string) *Models { return &Models{Dir: dir, Script: script, state: "missing"} }

func (m *Models) present() bool {
	for _, r := range required {
		if st, err := os.Stat(filepath.Join(m.Dir, r)); err != nil || st.Size() == 0 {
			return false
		}
	}
	return true
}

// Ready reports whether all models are on disk.
func (m *Models) Ready() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != "downloading" && m.present() {
		m.state, m.detail = "ready", ""
	}
	return m.state == "ready"
}

// Status returns the state and a human detail line.
func (m *Models) Status() (string, string) {
	m.Ready()
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state, m.detail
}

// EnsureAsync starts the download script unless the models are present or already downloading.
func (m *Models) EnsureAsync(ctx context.Context) {
	if m.Ready() {
		return
	}
	m.mu.Lock()
	if m.state == "downloading" {
		m.mu.Unlock()
		return
	}
	m.state, m.detail = "downloading", "starting download (~430 MB, first start only)"
	m.mu.Unlock()
	go func() {
		cmd := exec.CommandContext(ctx, "bash", m.Script)
		cmd.Env = append(os.Environ(), "MODELS_DIR="+m.Dir)
		out, _ := cmd.StdoutPipe()
		cmd.Stderr = cmd.Stdout
		err := cmd.Start()
		if err == nil {
			sc := bufio.NewScanner(out)
			for sc.Scan() {
				if l := strings.TrimSpace(sc.Text()); l != "" {
					m.mu.Lock()
					m.detail = l
					m.mu.Unlock()
				}
			}
			err = cmd.Wait()
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		switch {
		case err == nil && m.present():
			m.state, m.detail = "ready", ""
		case err == nil:
			m.state, m.detail = "failed", "download finished but files are missing"
		default:
			m.state = "failed"
			m.detail = "download failed (" + err.Error() + "): " + m.detail + " — restart the container to retry"
		}
	}()
}
