package main

// The scan: one pass over a directory tree, written straight out as ncdu's JSON export format
// (https://dev.yorhel.nl/ncdu/jsonfmt), so a snapshot also opens in ncdu itself (ncdu -f).
//
// To keep snapshots small, every directory is listed but not every file: in each directory the
// keepFiles biggest files and any file of minFile or more are listed, and the rest are folded into
// one entry named "(N smaller files)" carrying their total size and an "other": N field.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

type Options struct {
	Root      string   // where the tree is on this machine, e.g. /host
	RootName  string   // what to call it in the snapshot, e.g. "/"
	KeepFiles int      // files listed per directory (the biggest)
	MinFile   int64    // files at least this big are always listed
	Exclude   []string // paths as seen in the snapshot (e.g. /var/cache), not scanned
	AllowFS   []string // extra filesystem types to descend into (see fstypes)
}

// Progress is read while a scan runs.
type Progress struct {
	Dirs, Files, Bytes atomic.Int64
}

type Stats struct {
	Started      time.Time `json:"started"`
	Seconds      float64   `json:"seconds"`
	Dirs         int64     `json:"dirs"`
	Files        int64     `json:"files"`
	ApparentSize int64     `json:"apparentSize"` // sum of file sizes
	DiskUsage    int64     `json:"diskUsage"`    // blocks actually used
	Errors       int64     `json:"errors"`       // directories that couldn't be read
	Skipped      []string  `json:"skipped"`      // mount points not scanned, with why
}

type devIno struct{ dev, ino uint64 }

type scanner struct {
	opt      Options
	w        *bufio.Writer
	prog     *Progress
	st       *Stats
	seen     map[devIno]bool // hard-linked files already counted
	mounts   map[uint64]string
	exclude  map[string]bool
	allowFS  map[string]bool
	writeErr error
}

// Scan walks opt.Root and writes the snapshot to out.
func Scan(opt Options, out io.Writer, prog *Progress) (*Stats, error) {
	if prog == nil {
		prog = &Progress{}
	}
	if opt.RootName == "" {
		opt.RootName = "/"
	}
	s := &scanner{
		opt: opt, w: bufio.NewWriterSize(out, 1<<16), prog: prog,
		st:      &Stats{Started: time.Now().UTC(), Skipped: []string{}},
		seen:    map[devIno]bool{},
		mounts:  map[uint64]string{},
		exclude: map[string]bool{},
		allowFS: map[string]bool{},
	}
	for _, e := range opt.Exclude {
		if e = strings.TrimRight(strings.TrimSpace(e), "/"); e != "" {
			s.exclude[e] = true
		}
	}
	for _, f := range opt.AllowFS {
		s.allowFS[strings.TrimSpace(f)] = true
	}
	fi, err := os.Lstat(opt.Root)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", opt.Root)
	}
	root := statOf(fi)
	s.mounts[root.dev] = opt.RootName
	host, _ := os.Hostname()
	meta, _ := json.Marshal(map[string]any{
		"progname": "diskscan", "progver": version, "timestamp": s.st.Started.Unix(), "host": host,
	})
	s.put(`[1,2,`)
	s.put(string(meta))
	s.put(",")
	s.dir(opt.Root, opt.RootName, opt.RootName, root, root.dev, true)
	s.put("]\n")
	if err := s.w.Flush(); err != nil && s.writeErr == nil {
		s.writeErr = err
	}
	s.st.Seconds = time.Since(s.st.Started).Seconds()
	s.st.Dirs, s.st.Files = prog.Dirs.Load(), prog.Files.Load()
	return s.st, s.writeErr
}

func (s *scanner) put(str string) {
	if s.writeErr == nil {
		_, s.writeErr = s.w.WriteString(str)
	}
}

// entry writes one ncdu info block. extra holds optional fields.
func (s *scanner) entry(name string, st fstat, extra map[string]any) {
	m := map[string]any{"name": name, "asize": st.size, "dsize": st.blocks}
	for k, v := range extra {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	s.put(string(b))
}

// count adds a file to the totals once, even when it is hard-linked from several places.
// It returns false for a second sighting of a hard link (ncdu dedups these by dev+ino too).
func (s *scanner) count(st fstat) bool {
	if st.nlink > 1 {
		k := devIno{st.dev, st.ino}
		if s.seen[k] {
			return false
		}
		s.seen[k] = true
	}
	s.st.ApparentSize += st.size
	s.st.DiskUsage += st.blocks
	s.prog.Bytes.Add(st.blocks)
	return true
}

type fileEnt struct {
	name  string
	st    fstat
	extra map[string]any
	first bool // counted (not a repeat hard link)
}

// dir writes a directory as [info, children...]. path is on disk, shown is the snapshot path.
func (s *scanner) dir(path, name, shown string, st fstat, parentDev uint64, isRoot bool) {
	s.prog.Dirs.Add(1)
	s.count(st)
	info := map[string]any{}
	if !isRoot && st.dev != parentDev {
		info["dev"] = st.dev
	}
	if isRoot {
		info["dev"] = st.dev
	}
	ents, err := os.ReadDir(path)
	if err != nil {
		s.st.Errors++
		info["read_error"] = true
	}
	s.put("[")
	s.entry(name, st, info)

	var files []fileEnt
	type sub struct {
		name string
		st   fstat
	}
	var subs []sub
	for _, de := range ents {
		fi, err := de.Info() // lstat
		if err != nil {
			continue // vanished while scanning
		}
		cst := statOf(fi)
		switch {
		case fi.IsDir():
			subs = append(subs, sub{de.Name(), cst})
		default:
			s.prog.Files.Add(1)
			fe := fileEnt{name: de.Name(), st: cst, extra: map[string]any{}}
			if !fi.Mode().IsRegular() {
				fe.extra["notreg"] = true
			}
			if cst.nlink > 1 {
				fe.extra["ino"] = cst.ino
				fe.extra["hlnkc"] = true
				if cst.dev != st.dev {
					fe.extra["dev"] = cst.dev
				}
			}
			fe.first = s.count(cst)
			files = append(files, fe)
		}
	}

	// Biggest files first; list keepFiles of them plus any over minFile, fold the rest.
	sort.Slice(files, func(i, j int) bool {
		a, b := files[i].st, files[j].st
		if a.blocks != b.blocks {
			return a.blocks > b.blocks
		}
		if a.size != b.size {
			return a.size > b.size
		}
		return files[i].name < files[j].name
	})
	var otherN int64
	var other fstat
	for i, f := range files {
		if i < s.opt.KeepFiles || (s.opt.MinFile > 0 && f.st.blocks >= s.opt.MinFile) {
			s.put(",")
			s.entry(f.name, f.st, f.extra)
			continue
		}
		otherN++
		if f.first { // a repeat hard link adds nothing
			other.size += f.st.size
			other.blocks += f.st.blocks
		}
	}
	if otherN > 0 {
		s.put(",")
		s.entry(fmt.Sprintf("(%d smaller files)", otherN), other, map[string]any{"other": otherN})
	}

	for _, d := range subs {
		cpath := filepath.Join(path, d.name)
		cshown := strings.TrimRight(shown, "/") + "/" + d.name
		if s.exclude[cshown] {
			s.put(",")
			s.entry(d.name, d.st, map[string]any{"excluded": "pattern"})
			continue
		}
		if d.st.dev != st.dev { // a mount point
			if why := s.skipMount(cpath, cshown, d.st.dev); why != "" {
				s.put(",")
				s.entry(d.name, d.st, map[string]any{"excluded": why})
				continue
			}
		}
		s.put(",")
		s.dir(cpath, d.name, cshown, d.st, st.dev, false)
	}
	s.put("]")
}

// skipMount decides whether to descend into a mount point. It returns "" to scan it, or the ncdu
// "excluded" reason: kernfs for /proc, /sys and friends, otherfs for network shares, container
// layers, tmpfs and a second mount of a filesystem already scanned.
func (s *scanner) skipMount(path, shown string, dev uint64) string {
	fs := fsType(path)
	switch {
	case kernelFS[fs]:
		s.st.Skipped = append(s.st.Skipped, shown+" ("+fs+")")
		return "kernfs"
	case !diskFS[fs] && !s.allowFS[fs]:
		s.st.Skipped = append(s.st.Skipped, shown+" ("+fs+")")
		return "otherfs"
	}
	if first, ok := s.mounts[dev]; ok {
		s.st.Skipped = append(s.st.Skipped, shown+" (same filesystem as "+first+")")
		return "otherfs"
	}
	s.mounts[dev] = shown
	return ""
}
