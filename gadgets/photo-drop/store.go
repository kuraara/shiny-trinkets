package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var errTooManyRooms = errors.New("there are too many sessions running; end an old one first")

// store holds every session ("room") in memory and mirrors each to <data>/rooms/<code>/.
type store struct {
	cfg    config
	mu     sync.Mutex
	rooms  map[string]*room
	quit   chan struct{}
	once   sync.Once
	imgSem chan struct{} // limits concurrent image processing (the Pi has 4 cores)
}

func openStore(cfg config) (*store, error) {
	s := &store{cfg: cfg, rooms: map[string]*room{}, quit: make(chan struct{}), imgSem: make(chan struct{}, 2)}
	dir := filepath.Join(cfg.DataDir, "rooms")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		rdir := filepath.Join(dir, e.Name())
		r, err := loadRoom(rdir)
		if err != nil {
			log.Printf("skipping %s: %v", rdir, err)
			continue
		}
		if time.Since(r.Touched) > cfg.RoomTTL {
			log.Printf("session %s expired, deleting", r.Code)
			_ = os.RemoveAll(rdir)
			continue
		}
		s.rooms[r.Code] = r
	}
	return s, nil
}

func (s *store) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.rooms)
}

func (s *store) create(title string, set settings) (*room, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.rooms) >= s.cfg.MaxRooms {
		return nil, errTooManyRooms
	}
	code := newCode()
	for s.rooms[code] != nil {
		code = newCode()
	}
	r := newRoom(filepath.Join(s.cfg.DataDir, "rooms", code), code, title, set)
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		return nil, err
	}
	if err := r.save(); err != nil {
		return nil, err
	}
	s.rooms[code] = r
	return r, nil
}

func (s *store) get(code string) *room {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rooms[strings.ToUpper(code)]
}

// end closes a session for good and deletes its photos.
func (s *store) end(r *room) {
	s.mu.Lock()
	delete(s.rooms, r.Code)
	s.mu.Unlock()
	r.close()
	if err := os.RemoveAll(r.dir); err != nil {
		log.Printf("removing %s: %v", r.dir, err)
	}
}

func (s *store) all() []*room {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*room, 0, len(s.rooms))
	for _, r := range s.rooms {
		out = append(out, r)
	}
	return out
}

func (s *store) saveDirty() {
	for _, r := range s.all() {
		if err := r.saveIfDirty(); err != nil {
			log.Printf("saving session %s: %v", r.Code, err)
		}
	}
}

func (s *store) expire() {
	for _, r := range s.all() {
		if r.idleFor() > s.cfg.RoomTTL {
			log.Printf("session %s idle for %s, deleting", r.Code, s.cfg.RoomTTL)
			s.end(r)
		}
	}
}

// janitor saves changed sessions every couple of seconds and deletes idle ones.
func (s *store) janitor(ctx context.Context) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for n := 1; ; n++ {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.saveDirty()
		if n%30 == 0 {
			s.expire()
		}
	}
}

func (s *store) shutdown() { s.once.Do(func() { close(s.quit) }) }

// ---- random identifiers ----

const codeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789" // no I, L, O, 0 or 1

func newCode() string {
	b := make([]byte, 6)
	max := big.NewInt(int64(len(codeAlphabet)))
	for i := range b {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			panic(err)
		}
		b[i] = codeAlphabet[n.Int64()]
	}
	return string(b)
}

// newID is a 26-character lowercase base32 id, also used in photo file names.
func newID() string { return strings.ToLower(rand.Text()) }

func newSecret() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func writeFileAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(f.Name())
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return err
	}
	return os.Rename(f.Name(), path)
}

func loadRoom(dir string) (*room, error) {
	b, err := os.ReadFile(filepath.Join(dir, "room.json"))
	if err != nil {
		return nil, err
	}
	r := &room{}
	if err := json.Unmarshal(b, r); err != nil {
		return nil, err
	}
	if r.Code == "" || r.HostKey == "" {
		return nil, errors.New("room.json is incomplete")
	}
	r.dir = dir
	r.init()
	return r, nil
}
