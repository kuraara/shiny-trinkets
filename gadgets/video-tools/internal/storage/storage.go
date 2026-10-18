// Package storage owns everything written to the data volume: uploaded files, the read-only
// inbox, free-space checks and the guards that stop a job from filling the disk.
package storage

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// ErrNoSpace is returned when an operation would take the volume below the configured reserve.
var ErrNoSpace = errors.New("not enough free disk space")

// ErrTooLarge is returned when an upload exceeds MAX_UPLOAD_MB.
var ErrTooLarge = errors.New("upload exceeds the size limit")

var videoExt = map[string]bool{".mp4": true, ".mov": true, ".m4v": true, ".mkv": true, ".webm": true,
	".avi": true, ".mts": true, ".m2ts": true, ".ts": true, ".3gp": true, ".wmv": true, ".flv": true, ".mpg": true, ".mpeg": true}

// IsVideoName reports whether a file name has a common video extension.
func IsVideoName(name string) bool { return videoExt[strings.ToLower(filepath.Ext(name))] }

type Store struct {
	FilesDir string // uploaded inputs: <FilesDir>/<id>/<name>
	JobsDir  string
	TmpDir   string
	InboxDir string
	MinFree  int64
	MaxBody  int64
}

func New(dataDir, inbox string, minFree, maxBody int64) (*Store, error) {
	s := &Store{
		FilesDir: filepath.Join(dataDir, "files"),
		JobsDir:  filepath.Join(dataDir, "jobs"),
		TmpDir:   filepath.Join(dataDir, "tmp"),
		InboxDir: inbox,
		MinFree:  minFree,
		MaxBody:  maxBody,
	}
	for _, d := range []string{s.FilesDir, s.JobsDir, s.TmpDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Free returns the bytes available to us on the filesystem holding path.
func Free(path string) int64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return -1
	}
	return int64(st.Bavail) * int64(st.Bsize)
}

// Total returns the size of the filesystem holding path.
func Total(path string) int64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return -1
	}
	return int64(st.Blocks) * int64(st.Bsize)
}

// Reserve checks that `need` more bytes can be written while keeping the MinFree reserve.
func (s *Store) Reserve(need int64) error {
	free := Free(s.JobsDir)
	if free < 0 {
		return nil // can't tell; don't block
	}
	if free-need < s.MinFree {
		return fmt.Errorf("%w: need %s, %s free, %s kept in reserve (MIN_FREE_MB)",
			ErrNoSpace, Human(need), Human(free), Human(s.MinFree))
	}
	return nil
}

// File is an input clip, either uploaded or in the inbox.
type File struct {
	ID       string    `json:"id"`   // "f_<hex>" for uploads, "inbox:<rel path>" for inbox files
	Name     string    `json:"name"` // display name
	Size     int64     `json:"size"`
	Created  time.Time `json:"created"`
	Path     string    `json:"-"`
	Inbox    bool      `json:"inbox"`
	Uploaded bool      `json:"uploaded"`
}

// NewID returns a short random id with a prefix.
func NewID(prefix string) string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// SaveUploads streams every file part of a multipart request straight to disk (never into memory),
// enforcing the request size cap and the free-space reserve while writing. Non-file fields are returned.
func (s *Store) SaveUploads(w http.ResponseWriter, r *http.Request) ([]File, map[string][]string, error) {
	if r.ContentLength > 0 {
		if r.ContentLength > s.MaxBody {
			return nil, nil, fmt.Errorf("%w (%s > %s)", ErrTooLarge, Human(r.ContentLength), Human(s.MaxBody))
		}
		if err := s.Reserve(r.ContentLength); err != nil {
			return nil, nil, err
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.MaxBody)
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, nil, fmt.Errorf("expected a multipart upload: %w", err)
	}
	var files []File
	fields := map[string][]string{}
	cleanup := func() {
		for _, f := range files {
			_ = os.RemoveAll(filepath.Dir(f.Path))
		}
	}
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			cleanup()
			return nil, nil, uploadErr(err)
		}
		if part.FileName() == "" {
			v, _ := io.ReadAll(io.LimitReader(part, 64<<10))
			fields[part.FormName()] = append(fields[part.FormName()], string(v))
			continue
		}
		f, err := s.savePart(part)
		if err != nil {
			cleanup()
			return nil, nil, uploadErr(err)
		}
		if f.Size == 0 {
			_ = os.RemoveAll(filepath.Dir(f.Path))
			continue
		}
		files = append(files, f)
	}
	return files, fields, nil
}

func uploadErr(err error) error {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return fmt.Errorf("%w (limit %s, MAX_UPLOAD_MB)", ErrTooLarge, Human(mbe.Limit))
	}
	return err
}

// guardWriter aborts a write that would eat into the free-space reserve. Checked every 64 MB.
type guardWriter struct {
	f       *os.File
	s       *Store
	written int64
	next    int64
}

func (g *guardWriter) Write(p []byte) (int, error) {
	if g.written >= g.next {
		if err := g.s.Reserve(64 << 20); err != nil {
			return 0, err
		}
		g.next = g.written + 64<<20
	}
	n, err := g.f.Write(p)
	g.written += int64(n)
	return n, err
}

func (s *Store) savePart(part *multipart.Part) (File, error) {
	name := SafeName(part.FileName())
	id := NewID("f_")
	dir := filepath.Join(s.FilesDir, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return File{}, err
	}
	path := filepath.Join(dir, name)
	out, err := os.Create(path)
	if err != nil {
		return File{}, err
	}
	g := &guardWriter{f: out, s: s}
	n, err := io.CopyBuffer(g, part, make([]byte, 1<<20))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.RemoveAll(dir)
		return File{}, err
	}
	f := File{ID: id, Name: name, Size: n, Created: time.Now(), Path: path, Uploaded: true}
	meta, _ := json.Marshal(f)
	_ = os.WriteFile(filepath.Join(dir, ".meta.json"), meta, 0o644)
	return f, nil
}

// SafeName strips directories and awkward characters from an uploaded file name.
func SafeName(n string) string {
	n = filepath.Base(strings.ReplaceAll(n, "\\", "/"))
	var b strings.Builder
	for _, r := range n {
		switch {
		case r == '/' || r == 0 || r < 32:
		case strings.ContainsRune(`<>:"|?*'`, r):
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "" || out == "." || out == ".." {
		out = "upload"
	}
	if len(out) > 120 {
		ext := filepath.Ext(out)
		out = out[:120-len(ext)] + ext
	}
	return out
}

// Resolve turns a file id (upload or inbox) into a File, refusing anything outside our folders.
func (s *Store) Resolve(id string) (File, error) {
	if rel, ok := strings.CutPrefix(id, "inbox:"); ok {
		return s.resolveInbox(rel)
	}
	if !strings.HasPrefix(id, "f_") || strings.ContainsAny(id, `/\.`) {
		return File{}, fmt.Errorf("unknown file %q", id)
	}
	dir := filepath.Join(s.FilesDir, id)
	var f File
	if b, err := os.ReadFile(filepath.Join(dir, ".meta.json")); err == nil && json.Unmarshal(b, &f) == nil {
		f.Path = filepath.Join(dir, f.Name)
		if st, err := os.Stat(f.Path); err == nil {
			f.Size = st.Size()
			return f, nil
		}
	}
	return File{}, fmt.Errorf("file %q not found (uploads are deleted after JOB_TTL_HOURS)", id)
}

func (s *Store) resolveInbox(rel string) (File, error) {
	if s.InboxDir == "" {
		return File{}, errors.New("no inbox configured")
	}
	root, err := filepath.EvalSymlinks(s.InboxDir)
	if err != nil {
		return File{}, fmt.Errorf("inbox not available: %w", err)
	}
	p, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return File{}, fmt.Errorf("inbox file %q not found", rel)
	}
	if r, err := filepath.Rel(root, p); err != nil || r == ".." || strings.HasPrefix(r, "../") {
		return File{}, fmt.Errorf("inbox path %q escapes the inbox", rel)
	}
	st, err := os.Stat(p)
	if err != nil || st.IsDir() {
		return File{}, fmt.Errorf("inbox file %q not found", rel)
	}
	return File{ID: "inbox:" + filepath.ToSlash(rel), Name: filepath.Base(p), Size: st.Size(),
		Created: st.ModTime(), Path: p, Inbox: true}, nil
}

// List returns uploaded files and inbox videos, newest first.
func (s *Store) List() []File {
	var out []File
	ents, _ := os.ReadDir(s.FilesDir)
	for _, e := range ents {
		if f, err := s.Resolve(e.Name()); err == nil {
			out = append(out, f)
		}
	}
	if root, err := filepath.EvalSymlinks(s.InboxDir); err == nil {
		_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !IsVideoName(p) || strings.HasPrefix(d.Name(), ".") {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			if f, err := s.resolveInbox(filepath.ToSlash(rel)); err == nil {
				out = append(out, f)
			}
			return nil
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out
}

// DeleteUpload removes an uploaded file (never touches the inbox).
func (s *Store) DeleteUpload(id string) error {
	if !strings.HasPrefix(id, "f_") || strings.ContainsAny(id, `/\.`) {
		return fmt.Errorf("can only delete uploaded files")
	}
	return os.RemoveAll(filepath.Join(s.FilesDir, id))
}

// SweepUploads deletes uploads older than ttl unless inUse reports them as still needed,
// and clears stale scratch files.
func (s *Store) SweepUploads(ttl time.Duration, inUse func(id string) bool) (removed int) {
	ents, _ := os.ReadDir(s.FilesDir)
	for _, e := range ents {
		info, err := e.Info()
		if err != nil || time.Since(info.ModTime()) < ttl || inUse(e.Name()) {
			continue
		}
		if os.RemoveAll(filepath.Join(s.FilesDir, e.Name())) == nil {
			removed++
		}
	}
	tmp, _ := os.ReadDir(s.TmpDir)
	for _, e := range tmp {
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > 6*time.Hour {
			_ = os.RemoveAll(filepath.Join(s.TmpDir, e.Name()))
		}
	}
	return removed
}

// DirSize sums file sizes under a directory.
func DirSize(dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if i, err := d.Info(); err == nil {
				n += i.Size()
			}
		}
		return nil
	})
	return n
}

// Human formats a byte count.
func Human(n int64) string {
	const u = 1024
	if n < u {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(u), 0
	for m := n / u; m >= u; m /= u {
		div *= u
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
