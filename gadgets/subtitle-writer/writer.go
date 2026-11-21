package main

// The one thing this service does: put a subtitle file next to a video, safely.
//
// Rules, all enforced here:
//   - only `.srt` files are written, with a fixed name built from the video's name, a label
//     and a language (see sidecarName); callers never choose the file name;
//   - the video must exist, be a regular file with a video extension, and resolve (symlinks
//     and all) to a place inside the media root; so must its folder;
//   - a file that is already there is only replaced if this service wrote it (it is in the
//     manifest) and it hasn't been changed since; otherwise the call is refused;
//   - only files in the manifest can be removed, and only if they're unchanged.

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const maxSRT = 4 << 20 // bytes

var (
	videoExt = map[string]bool{".mkv": true, ".mp4": true, ".m4v": true, ".avi": true, ".mov": true,
		".ts": true, ".m2ts": true, ".webm": true, ".wmv": true, ".mpg": true, ".mpeg": true}
	labelRe = regexp.MustCompile(`^[a-z0-9]{1,24}$`)
	langRe  = regexp.MustCompile(`^[a-z]{2,3}$`)
	cueRe   = regexp.MustCompile(`\d+:\d\d:\d\d[,.]\d{1,3}\s*-->\s*\d+:\d\d:\d\d[,.]\d{1,3}`)
)

type writer struct {
	root string // media root, absolute, symlinks resolved
	man  *manifest
}

func newWriter(root string, man *manifest) (*writer, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	return &writer{root: real, man: man}, nil
}

// nameStyle picks the sidecar naming (NAME_STYLE). "dot" (default):
// "<video name>.<label>.<lang>.srt", e.g. "Show.S01E01.whisper.en.srt". Plex matches a sidecar
// when its name is the video's name exactly, followed by dot-separated parts with the language
// last (".en", ".en.forced", ".hi.eng"), so the label goes between the video name and the language.
// "dash" gives "<video name>-<label>.<lang>.srt", which reads nicer but changes the video name
// part, so Plex may not match it; it's there in case a Plex version is found to accept it.
var nameStyle = "dot"

// sidecarName is the subtitle's file name for a video.
func sidecarName(video, label, lang string) string {
	base := strings.TrimSuffix(filepath.Base(video), filepath.Ext(video))
	sep := "."
	if nameStyle == "dash" {
		sep = "-"
	}
	return base + sep + label + "." + lang + ".srt"
}

// inside reports whether p (absolute, cleaned) is the root or below it.
func (w *writer) inside(p string) bool {
	rel, err := filepath.Rel(w.root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// cleanRel checks a path given relative to the media root: no absolute paths, no "..",
// no empty or odd parts. It returns it cleaned, with forward slashes.
func cleanRel(rel string) (string, error) {
	if rel == "" || strings.ContainsRune(rel, 0) || strings.Contains(rel, `\`) {
		return "", errors.New("give the path relative to the media root, with forward slashes")
	}
	if strings.HasPrefix(rel, "/") {
		return "", errors.New("the path must be relative to the media root, not absolute")
	}
	for _, part := range strings.Split(rel, "/") {
		if part == ".." {
			return "", errors.New(`".." is not allowed in paths`)
		}
	}
	c := filepath.ToSlash(filepath.Clean(rel))
	if c == "." || strings.HasPrefix(c, "../") {
		return "", errors.New("bad path")
	}
	return c, nil
}

// resolveVideo finds the video and returns its real absolute path.
func (w *writer) resolveVideo(rel string) (string, error) {
	c, err := cleanRel(rel)
	if err != nil {
		return "", err
	}
	if !videoExt[strings.ToLower(filepath.Ext(c))] {
		return "", fmt.Errorf("%q is not a video file (allowed: mkv, mp4, m4v, avi, mov, ts, m2ts, webm, wmv, mpg)", c)
	}
	real, err := filepath.EvalSymlinks(filepath.Join(w.root, filepath.FromSlash(c)))
	if err != nil {
		return "", fmt.Errorf("no such video: %s", c)
	}
	if !w.inside(real) {
		return "", errors.New("that video resolves to somewhere outside the media root")
	}
	fi, err := os.Stat(real)
	if err != nil || !fi.Mode().IsRegular() {
		return "", fmt.Errorf("no such video: %s", c)
	}
	return real, nil
}

func checkSRT(srt string) error {
	switch {
	case len(srt) == 0:
		return errors.New("the subtitle text is empty")
	case len(srt) > maxSRT:
		return fmt.Errorf("the subtitle text is over %d MB", maxSRT>>20)
	case !utf8.ValidString(srt):
		return errors.New("the subtitle text must be UTF-8")
	case !cueRe.MatchString(srt):
		return errors.New("that doesn't look like SRT: no 00:00:00,000 --> 00:00:00,000 timings")
	}
	return nil
}

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// Put writes the subtitle next to the video and records it in the manifest.
func (w *writer) Put(video, srt, label, lang string) (Entry, error) {
	label, lang = strings.ToLower(strings.TrimSpace(label)), strings.ToLower(strings.TrimSpace(lang))
	if lang == "" {
		lang = "en"
	}
	if !labelRe.MatchString(label) {
		return Entry{}, errors.New("label must be 1-24 lowercase letters or digits, e.g. whisper or translated")
	}
	if !langRe.MatchString(lang) {
		return Entry{}, errors.New("lang must be a 2- or 3-letter ISO 639 code, e.g. en")
	}
	if err := checkSRT(srt); err != nil {
		return Entry{}, err
	}
	srt = strings.TrimPrefix(srt, "\ufeff")
	w.man.mu.Lock()
	defer w.man.mu.Unlock()
	vreal, err := w.resolveVideo(video)
	if err != nil {
		return Entry{}, err
	}
	dir := filepath.Dir(vreal)
	target := filepath.Join(dir, sidecarName(vreal, label, lang))
	if !w.inside(target) || filepath.Ext(target) != ".srt" {
		return Entry{}, errors.New("refusing: the subtitle would land outside the media root")
	}
	rel, _ := filepath.Rel(w.root, target)
	rel = filepath.ToSlash(rel)
	prev, ours := w.man.entries[rel]
	if fi, err := os.Lstat(target); err == nil {
		if !ours {
			return Entry{}, fmt.Errorf("refusing: %s already exists and this service didn't write it", rel)
		}
		if !fi.Mode().IsRegular() {
			return Entry{}, fmt.Errorf("refusing: %s is not a regular file", rel)
		}
		if b, err := os.ReadFile(target); err != nil || sum(b) != prev.SHA256 {
			return Entry{}, fmt.Errorf("refusing: %s was changed since this service wrote it", rel)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Entry{}, err
	}
	data := []byte(srt)
	var rnd [6]byte
	rand.Read(rnd[:])
	tmp := filepath.Join(dir, ".subtitle-writer-"+hex.EncodeToString(rnd[:])+".tmp")
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o664)
	if err != nil {
		return Entry{}, fmt.Errorf("can't write in that folder: %v", err)
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, target)
	}
	if err != nil {
		os.Remove(tmp)
		return Entry{}, err
	}
	vrel, _ := filepath.Rel(w.root, vreal)
	e := Entry{Path: rel, Video: filepath.ToSlash(vrel), Label: label, Lang: lang, Size: len(data),
		SHA256: sum(data), Written: time.Now().UTC().Truncate(time.Second), Replaced: ours}
	w.man.entries[rel] = e
	if err := w.man.saveLocked(); err != nil {
		return e, fmt.Errorf("written, but the manifest couldn't be saved: %v", err)
	}
	return e, nil
}

// Remove deletes a subtitle this service wrote, if it hasn't been changed since.
func (w *writer) Remove(rel string) (Entry, error) {
	c, err := cleanRel(rel)
	if err != nil {
		return Entry{}, err
	}
	w.man.mu.Lock()
	defer w.man.mu.Unlock()
	e, ok := w.man.entries[c]
	if !ok {
		return Entry{}, fmt.Errorf("refusing: %s is not a file this service wrote", c)
	}
	p := filepath.Join(w.root, filepath.FromSlash(c))
	if filepath.Ext(p) != ".srt" || !w.inside(p) {
		return Entry{}, errors.New("refusing: bad path")
	}
	fi, err := os.Lstat(p)
	switch {
	case errors.Is(err, os.ErrNotExist):
		// already gone: just forget it
	case err != nil:
		return Entry{}, err
	case !fi.Mode().IsRegular():
		return Entry{}, fmt.Errorf("refusing: %s is not a regular file", c)
	default:
		if real, err := filepath.EvalSymlinks(filepath.Dir(p)); err != nil || !w.inside(real) {
			return Entry{}, errors.New("refusing: that folder resolves outside the media root")
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return Entry{}, err
		}
		if sum(b) != e.SHA256 {
			return Entry{}, fmt.Errorf("refusing: %s was changed since this service wrote it; remove it by hand if you mean to", c)
		}
		if err := os.Remove(p); err != nil {
			return Entry{}, err
		}
	}
	delete(w.man.entries, c)
	return e, w.man.saveLocked()
}
