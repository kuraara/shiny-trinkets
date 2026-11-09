package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), size), 0o644); err != nil {
		t.Fatal(err)
	}
}

// node is a parsed ncdu directory: its info block and its children.
type node struct {
	info  map[string]any
	files []map[string]any
	dirs  []*node
}

func parse(t *testing.T, raw []byte) (meta map[string]any, root *node) {
	t.Helper()
	var top []json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, raw)
	}
	if len(top) != 4 || string(top[0]) != "1" || string(top[1]) != "2" {
		t.Fatalf("not ncdu export v1.2: %s", raw[:min(len(raw), 80)])
	}
	json.Unmarshal(top[2], &meta)
	return meta, parseDir(t, top[3])
}

func parseDir(t *testing.T, raw json.RawMessage) *node {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		t.Fatal(err)
	}
	n := &node{}
	json.Unmarshal(items[0], &n.info)
	for _, it := range items[1:] {
		if bytes.HasPrefix(bytes.TrimSpace(it), []byte("[")) {
			n.dirs = append(n.dirs, parseDir(t, it))
		} else {
			var f map[string]any
			json.Unmarshal(it, &f)
			n.files = append(n.files, f)
		}
	}
	return n
}

func (n *node) dir(name string) *node {
	for _, d := range n.dirs {
		if d.info["name"] == name {
			return d
		}
	}
	return nil
}

func TestScan(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "big.bin"), 300_000)
	for i := 0; i < 30; i++ {
		write(t, filepath.Join(root, "many", fmt.Sprintf("f%02d.txt", i)), 1000+i)
	}
	write(t, filepath.Join(root, "skipme", "x"), 5000)
	write(t, filepath.Join(root, "a", "orig"), 200_000)
	if err := os.Link(filepath.Join(root, "a", "orig"), filepath.Join(root, "a", "link")); err != nil {
		t.Fatal(err)
	}
	os.Symlink("big.bin", filepath.Join(root, "sym"))

	var out bytes.Buffer
	st, err := Scan(Options{Root: root, RootName: "/", KeepFiles: 5, MinFile: 250_000, Exclude: []string{"/skipme"}}, &out, nil)
	if err != nil {
		t.Fatal(err)
	}
	meta, top := parse(t, out.Bytes())
	if meta["progname"] != "diskscan" {
		t.Errorf("meta: %v", meta)
	}
	if top.info["name"] != "/" {
		t.Errorf("root name %v", top.info["name"])
	}

	// many/: 5 biggest files listed, the other 25 folded into one entry
	many := top.dir("many")
	if many == nil || len(many.files) != 6 {
		t.Fatalf("many/: want 5 files + 1 fold, got %v", many)
	}
	fold := many.files[5]
	if fold["other"] != float64(25) || !strings.HasPrefix(fold["name"].(string), "(25 smaller") {
		t.Errorf("fold entry: %v", fold)
	}
	if many.files[0]["name"] != "f29.txt" {
		t.Errorf("biggest first: %v", many.files[0])
	}

	// excluded folder is listed, marked, not descended
	if top.dir("skipme") != nil {
		t.Error("skipme/ was scanned")
	}
	found := false
	for _, f := range top.files {
		if f["name"] == "skipme" && f["excluded"] == "pattern" {
			found = true
		}
		if f["name"] == "sym" && f["notreg"] != true {
			t.Errorf("symlink not marked notreg: %v", f)
		}
	}
	if !found {
		t.Error("skipme not marked excluded")
	}

	// hard link: both names listed with ino/hlnkc, size counted once
	a := top.dir("a")
	if len(a.files) != 2 || a.files[0]["hlnkc"] != true || a.files[0]["ino"] != a.files[1]["ino"] {
		t.Errorf("hard links: %v", a.files)
	}
	want := int64(300_000 + 200_000 + 5000) // big + one copy of orig (+ skipme's dir entry aside)
	if st.ApparentSize < want-5000 || st.ApparentSize > want+100_000 {
		t.Errorf("apparent size %d, want about %d (hard link counted once)", st.ApparentSize, want)
	}
	if st.Files != 34 || st.Dirs != 3 {
		t.Errorf("counts: %d files, %d dirs", st.Files, st.Dirs)
	}
}

func TestNextRun(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, loc) // a Sunday
	w := &worker{cfg: Config{ScanAt: "03:30", ScanDays: map[string]bool{}}}
	if got := w.nextRun(now); !got.Equal(time.Date(2026, 11, 9, 3, 30, 0, 0, loc)) {
		t.Errorf("daily: %v", got)
	}
	w.cfg.ScanDays = map[string]bool{"sun": true}
	if got := w.nextRun(now); !got.Equal(time.Date(2026, 11, 15, 3, 30, 0, 0, loc)) {
		t.Errorf("weekly: %v", got)
	}
	early := time.Date(2026, 11, 8, 1, 0, 0, 0, loc)
	if got := w.nextRun(early); !got.Equal(time.Date(2026, 11, 8, 3, 30, 0, 0, loc)) {
		t.Errorf("later today: %v", got)
	}
}
