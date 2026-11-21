package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const srt = "1\n00:00:01,000 --> 00:00:02,000\nHello.\n"

func setupT(t *testing.T) (*writer, string, string) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "media")
	os.MkdirAll(filepath.Join(root, "TvShows", "Show", "Season 1"), 0o755)
	os.WriteFile(filepath.Join(root, "TvShows", "Show", "Season 1", "Show.S01E01.mkv"), []byte("x"), 0o644)
	outside := filepath.Join(base, "outside")
	os.MkdirAll(outside, 0o755)
	os.WriteFile(filepath.Join(outside, "Secret.mkv"), []byte("x"), 0o644)
	data := t.TempDir()
	w, err := setup(root, filepath.Join(data, "written.json"))
	if err != nil {
		t.Fatal(err)
	}
	return w, root, data
}

func TestPutListRemove(t *testing.T) {
	w, root, data := setupT(t)
	e, err := w.Put("TvShows/Show/Season 1/Show.S01E01.mkv", srt, "whisper", "")
	if err != nil {
		t.Fatal(err)
	}
	want := "TvShows/Show/Season 1/Show.S01E01.whisper.en.srt"
	if e.Path != want {
		t.Fatalf("path %q, want %q", e.Path, want)
	}
	if b, _ := os.ReadFile(filepath.Join(root, want)); string(b) != srt {
		t.Fatalf("content %q", b)
	}
	// writing again replaces our own file
	if e, err = w.Put("TvShows/Show/Season 1/Show.S01E01.mkv", srt+"\n2\n00:00:03,000 --> 00:00:04,000\nBye.\n", "whisper", "en"); err != nil || !e.Replaced {
		t.Fatalf("replace: %v %+v", err, e)
	}
	// the manifest survives a restart
	w2, _ := setup(root, filepath.Join(data, "written.json"))
	if l := w2.man.List("", ""); len(l) != 1 || l[0].Path != want {
		t.Fatalf("reloaded manifest: %+v", l)
	}
	// an edited file is neither replaced nor removed
	os.WriteFile(filepath.Join(root, want), []byte(srt+"edited"), 0o644)
	if _, err := w2.Put("TvShows/Show/Season 1/Show.S01E01.mkv", srt, "whisper", "en"); err == nil {
		t.Fatal("replaced an edited file")
	}
	if _, err := w2.Remove(want); err == nil {
		t.Fatal("removed an edited file")
	}
	os.WriteFile(filepath.Join(root, want), []byte(srt), 0o644)
	w2.man.entries[want] = Entry{Path: want, SHA256: sum([]byte(srt))}
	if _, err := w2.Remove(want); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, want)); !os.IsNotExist(err) {
		t.Fatal("file still there")
	}
}

func TestRefusals(t *testing.T) {
	w, root, _ := setupT(t)
	dir := filepath.Join(root, "TvShows", "Show", "Season 1")
	// a file someone else made
	os.WriteFile(filepath.Join(dir, "Show.S01E01.translated.en.srt"), []byte("theirs"), 0o644)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644)
	// symlinks out of the root
	os.Symlink(filepath.Join(root, "..", "outside", "Secret.mkv"), filepath.Join(dir, "Link.mkv"))
	os.Symlink(filepath.Join(root, "..", "outside"), filepath.Join(root, "Out"))
	cases := []struct{ video, label, lang, srt string }{
		{"TvShows/Show/Season 1/Show.S01E01.mkv", "translated", "en", srt}, // exists, not ours
		{"TvShows/Show/Season 1/Missing.mkv", "whisper", "en", srt},
		{"TvShows/Show/Season 1/notes.txt", "whisper", "en", srt},
		{"../outside/Secret.mkv", "whisper", "en", srt},
		{"TvShows/../../outside/Secret.mkv", "whisper", "en", srt},
		{"/etc/passwd.mkv", "whisper", "en", srt},
		{"TvShows/Show/Season 1/Link.mkv", "whisper", "en", srt},
		{"Out/Secret.mkv", "whisper", "en", srt},
		{"TvShows/Show/Season 1/Show.S01E01.mkv", "../x", "en", srt},
		{"TvShows/Show/Season 1/Show.S01E01.mkv", "Whisper/x", "en", srt},
		{"TvShows/Show/Season 1/Show.S01E01.mkv", "whisper", "e/n", srt},
		{"TvShows/Show/Season 1/Show.S01E01.mkv", "whisper", "en", "not a subtitle"},
		{"TvShows/Show/Season 1/Show.S01E01.mkv", "whisper", "en", ""},
	}
	for _, c := range cases {
		if e, err := w.Put(c.video, c.srt, c.label, c.lang); err == nil {
			t.Errorf("Put(%q, label %q, lang %q) wrote %s", c.video, c.label, c.lang, e.Path)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "Show.S01E01.translated.en.srt")); string(b) != "theirs" {
		t.Fatal("someone else's file was changed")
	}
	for _, p := range []string{"TvShows/Show/Season 1/Show.S01E01.translated.en.srt", "TvShows/Show/Season 1/notes.txt", "../outside/Secret.mkv"} {
		if _, err := w.Remove(p); err == nil {
			t.Errorf("Remove(%q) succeeded", p)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "notes.txt")); err != nil {
		t.Fatal("notes.txt was removed")
	}
	if ents, _ := os.ReadDir(filepath.Join(root, "..", "outside")); len(ents) != 1 {
		t.Fatal("something was written outside")
	}
	// no temp files left behind
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatal("temp file left: ", e.Name())
		}
	}
}

func TestHTTPAndMCP(t *testing.T) {
	w, _, _ := setupT(t)
	h := withAuth("tok", routes(w))
	do := func(method, path, body, tok string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if tok != "" {
			r.Header.Set("Authorization", "Bearer "+tok)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}
	if do("GET", "/healthz", "", "").Code != 200 {
		t.Fatal("healthz")
	}
	if do("GET", "/api/subtitles", "", "").Code != 401 || do("POST", "/mcp", "{}", "bad").Code != 401 {
		t.Fatal("no auth accepted")
	}
	body, _ := json.Marshal(putReq{Video: "TvShows/Show/Season 1/Show.S01E01.mkv", SRT: srt, Label: "whisper"})
	if rec := do("POST", "/api/subtitles", string(body), "tok"); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	call, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "list_subtitles", "arguments": map[string]any{"label": "whisper"}}})
	rec := do("POST", "/mcp", string(call), "tok")
	if !bytes.Contains(rec.Body.Bytes(), []byte(`Show.S01E01.whisper.en.srt`)) {
		t.Fatal(rec.Body.String())
	}
	tl, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	if rec := do("POST", "/mcp", string(tl), "tok"); !bytes.Contains(rec.Body.Bytes(), []byte("put_subtitle")) {
		t.Fatal(rec.Body.String())
	}
}
