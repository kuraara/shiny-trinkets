package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type settings struct {
	RequireNames        bool   `json:"requireNames"`        // guests must give a name (otherwise "Guest 3" is fine)
	ApproveParticipants bool   `json:"approveParticipants"` // host lets each guest in
	ShowQR              bool   `json:"showQR"`              // join code on the desk display
	AutoArrange         int    `json:"autoArrange"`         // minutes between automatic re-arranges; 0 = off
	AutoStyle           string `json:"autoStyle"`           // "shuffle" or "tidy"
}

const (
	statusWaiting  = "waiting"
	statusAdmitted = "admitted"
	statusDenied   = "denied"
	statusKicked   = "kicked"

	photoPending  = "pending"
	photoApproved = "approved"
	photoRejected = "rejected"
	photoRemoved  = "removed"

	hostPID = "host"
)

type participant struct {
	ID        string    `json:"id"`
	Token     string    `json:"token"`
	Name      string    `json:"name"`
	Anonymous bool      `json:"anonymous"`
	Status    string    `json:"status"`
	Joined    time.Time `json:"joined"`
}

type photo struct {
	ID       string    `json:"id"`
	PID      string    `json:"pid"`
	Caption  string    `json:"caption,omitempty"`
	Status   string    `json:"status"`
	Created  time.Time `json:"created"`
	Approved time.Time `json:"approvedAt,omitzero"`
	W        int       `json:"w"`
	H        int       `json:"h"`
	X        float64   `json:"x"`   // 0..1 across the free space on the desk
	Y        float64   `json:"y"`   // 0..1 down the free space on the desk
	Rot      float64   `json:"rot"` // degrees, -15..15
	Z        int       `json:"z"`   // stacking order; newest on top
}

type role int

const (
	roleNone role = iota
	roleHost
	roleDisplay
	roleParticipant
)

func (r role) String() string {
	return [...]string{"none", "host", "display", "participant"}[r]
}

type subscriber struct {
	ch chan struct{}
}

type room struct {
	mu      sync.Mutex
	dir     string
	subs    map[*subscriber]struct{}
	byToken map[string]*participant
	byID    map[string]*participant
	closed  bool
	dirty   bool
	version int64

	Code       string         `json:"code"`
	Title      string         `json:"title"`
	HostKey    string         `json:"hostKey"`
	DisplayKey string         `json:"displayKey"`
	Settings   settings       `json:"settings"`
	Created    time.Time      `json:"created"`
	Touched    time.Time      `json:"touched"`
	People     []*participant `json:"people"`
	Photos     []*photo       `json:"photos"`
	NextZ      int            `json:"nextZ"`
	Guests     int            `json:"guests"`
	Arranged   time.Time      `json:"arranged,omitzero"` // last re-arrange, for the timer
}

var (
	errClosed        = errors.New("this session has ended")
	errNotFound      = errors.New("not found")
	errNameRequired  = errors.New("please enter your name")
	errRoomFull      = errors.New("this session is full")
	errNotAdmitted   = errors.New("the host hasn't let you in yet")
	errTooManyPhotos = errors.New("you've sent as many photos as this session allows")
	errTooManyQueued = errors.New("wait for the host to look at your other photos first")
	errDeskFull      = errors.New("this session has all the photos it can hold")
)

func newRoom(dir, code, title string, set settings) *room {
	now := time.Now().UTC()
	r := &room{
		dir: dir, Code: code, Title: title, Settings: set,
		HostKey: newSecret(), DisplayKey: newSecret(),
		Created: now, Touched: now,
	}
	r.init()
	return r
}

func (r *room) init() {
	r.subs = map[*subscriber]struct{}{}
	r.byToken = map[string]*participant{}
	r.byID = map[string]*participant{}
	for _, p := range r.People {
		r.byToken[p.Token] = p
		r.byID[p.ID] = p
	}
}

// changed must be called with r.mu held. It wakes every live stream.
func (r *room) changed() {
	r.version++
	r.dirty = true
	r.Touched = time.Now().UTC()
	for s := range r.subs {
		select {
		case s.ch <- struct{}{}:
		default: // a wake-up is already queued; the stream will read the latest state
		}
	}
}

func (r *room) subscribe() *subscriber {
	s := &subscriber{ch: make(chan struct{}, 1)}
	r.mu.Lock()
	r.subs[s] = struct{}{}
	r.mu.Unlock()
	return s
}

func (r *room) unsubscribe(s *subscriber) {
	r.mu.Lock()
	delete(r.subs, s)
	r.mu.Unlock()
}

func (r *room) close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	r.changed()
	r.dirty = false
}

func (r *room) isClosed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closed
}

func (r *room) idleFor() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return time.Since(r.Touched)
}

func (r *room) save() error {
	r.mu.Lock()
	b, err := json.MarshalIndent(r, "", " ")
	r.mu.Unlock()
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(r.dir, "room.json"), b)
}

func (r *room) saveIfDirty() error {
	r.mu.Lock()
	if !r.dirty || r.closed {
		r.mu.Unlock()
		return nil
	}
	r.dirty = false
	b, err := json.MarshalIndent(r, "", " ")
	r.mu.Unlock()
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(r.dir, "room.json"), b)
}

// auth works out who is calling from the key they sent.
func (r *room) auth(key string) (role, *participant) {
	if key == "" {
		return roleNone, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if subtle.ConstantTimeCompare([]byte(key), []byte(r.HostKey)) == 1 {
		return roleHost, nil
	}
	if subtle.ConstantTimeCompare([]byte(key), []byte(r.DisplayKey)) == 1 {
		return roleDisplay, nil
	}
	if p := r.byToken[key]; p != nil {
		return roleParticipant, p
	}
	return roleNone, nil
}

func (r *room) info() (title string, set settings) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.Title, r.Settings
}

// ---- guests ----

func (r *room) join(name string, maxPeople int) (*participant, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, errClosed
	}
	if r.Settings.RequireNames && len([]rune(name)) < 2 {
		return nil, errNameRequired
	}
	if len(r.People) >= maxPeople {
		return nil, errRoomFull
	}
	p := &participant{ID: newID()[:12], Token: newSecret(), Name: name, Joined: time.Now().UTC(), Status: statusAdmitted}
	if name == "" {
		r.Guests++
		p.Name = fmt.Sprintf("Guest %d", r.Guests)
		p.Anonymous = true
	}
	if r.Settings.ApproveParticipants {
		p.Status = statusWaiting
	}
	r.People = append(r.People, p)
	r.byToken[p.Token] = p
	r.byID[p.ID] = p
	r.changed()
	return p, nil
}

// person applies a host decision about a guest: admit, deny or kick.
func (r *room) person(pid, action string) (removed []string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, errClosed
	}
	p := r.byID[pid]
	if p == nil {
		return nil, errNotFound
	}
	switch action {
	case "admit":
		p.Status = statusAdmitted
	case "deny":
		if p.Status != statusWaiting {
			return nil, errors.New("only guests who are waiting can be turned away")
		}
		p.Status = statusDenied
	case "kick":
		p.Status = statusKicked
		for _, ph := range r.Photos {
			if ph.PID == p.ID && ph.Status == photoPending {
				ph.Status = photoRejected
				removed = append(removed, ph.ID)
			}
		}
	default:
		return nil, errNotFound
	}
	r.changed()
	return removed, nil
}

func (r *room) admitAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.People {
		if p.Status == statusWaiting {
			p.Status = statusAdmitted
		}
	}
	r.changed()
}

type settingsPatch struct {
	Title               *string `json:"title"`
	RequireNames        *bool   `json:"requireNames"`
	ApproveParticipants *bool   `json:"approveParticipants"`
	ShowQR              *bool   `json:"showQR"`
	AutoArrange         *int    `json:"autoArrange"`
	AutoStyle           *string `json:"autoStyle"`
}

func (r *room) update(p settingsPatch) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return errClosed
	}
	if p.Title != nil {
		if t := cleanText(*p.Title, 60); t != "" {
			r.Title = t
		}
	}
	if p.RequireNames != nil {
		r.Settings.RequireNames = *p.RequireNames
	}
	if p.ShowQR != nil {
		r.Settings.ShowQR = *p.ShowQR
	}
	if p.AutoArrange != nil {
		r.Settings.AutoArrange = min(max(*p.AutoArrange, 0), 24*60)
		r.Arranged = time.Now().UTC() // the timer starts now
	}
	if p.AutoStyle != nil && validStyle(*p.AutoStyle) {
		r.Settings.AutoStyle = *p.AutoStyle
	}
	if p.ApproveParticipants != nil {
		r.Settings.ApproveParticipants = *p.ApproveParticipants
		if !*p.ApproveParticipants { // nobody left stuck in the lobby
			for _, q := range r.People {
				if q.Status == statusWaiting {
					q.Status = statusAdmitted
				}
			}
		}
	}
	r.changed()
	return nil
}

// ---- photos ----

// canUpload is checked before the (slow) image processing and again when the photo is added.
func (r *room) canUpload(p *participant, host bool, cfg config) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.canUploadLocked(p, host, cfg)
}

func (r *room) canUploadLocked(p *participant, host bool, cfg config) error {
	if r.closed {
		return errClosed
	}
	live := 0
	for _, ph := range r.Photos {
		if ph.Status == photoPending || ph.Status == photoApproved {
			live++
		}
	}
	if live >= cfg.MaxPhotos {
		return errDeskFull
	}
	if host {
		return nil
	}
	if p.Status != statusAdmitted {
		return errNotAdmitted
	}
	sent, queued := 0, 0
	for _, ph := range r.Photos {
		if ph.PID == p.ID {
			sent++
			if ph.Status == photoPending {
				queued++
			}
		}
	}
	if sent >= cfg.MaxPerPerson {
		return errTooManyPhotos
	}
	if queued >= cfg.MaxPendingEach {
		return errTooManyQueued
	}
	return nil
}

// addPhoto records a processed photo. Host photos skip the queue.
func (r *room) addPhoto(p *participant, host bool, id, caption string, w, h int, cfg config) (*photo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.canUploadLocked(p, host, cfg); err != nil {
		return nil, err
	}
	ph := &photo{ID: id, Caption: caption, Status: photoPending, Created: time.Now().UTC(), W: w, H: h}
	if host {
		ph.PID = hostPID
		r.approveLocked(ph)
	} else {
		ph.PID = p.ID
	}
	r.Photos = append(r.Photos, ph)
	r.changed()
	return ph, nil
}

func (r *room) findPhoto(id string) *photo {
	for _, ph := range r.Photos {
		if ph.ID == id {
			return ph
		}
	}
	return nil
}

// photoVisible reports whether a photo file may be served (it is waiting or on the desk).
func (r *room) photoVisible(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	ph := r.findPhoto(id)
	return ph != nil && (ph.Status == photoPending || ph.Status == photoApproved)
}

func (r *room) approveLocked(ph *photo) {
	ph.X, ph.Y = r.place()
	ph.Rot = math.Round((rand.Float64()*30-15)*10) / 10
	r.NextZ++
	ph.Z = r.NextZ
	ph.Status = photoApproved
	ph.Approved = time.Now().UTC()
}

// place picks a spot for a new photo: the best of a few random spots, judged by distance
// from the most recent photos, so a new print doesn't land squarely on the last one.
func (r *room) place() (x, y float64) {
	var recent []*photo
	for _, ph := range r.Photos {
		if ph.Status == photoApproved {
			recent = append(recent, ph)
		}
	}
	sort.Slice(recent, func(i, j int) bool { return recent[i].Z > recent[j].Z })
	if len(recent) > 10 {
		recent = recent[:10]
	}
	best := -1.0
	for i := 0; i < 6; i++ {
		cx, cy := rand.Float64(), rand.Float64()
		d := 10.0
		for _, ph := range recent {
			d = math.Min(d, math.Hypot(cx-ph.X, cy-ph.Y))
		}
		if d > best {
			best, x, y = d, cx, cy
		}
	}
	return math.Round(x*1000) / 1000, math.Round(y*1000) / 1000
}

// photoAction applies approve / reject / remove. It returns ids whose files should be deleted.
func (r *room) photoAction(id, action string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, errClosed
	}
	ph := r.findPhoto(id)
	if ph == nil {
		return nil, errNotFound
	}
	switch {
	case action == "approve" && ph.Status == photoPending:
		r.approveLocked(ph)
	case action == "reject" && ph.Status == photoPending:
		ph.Status = photoRejected
	case action == "remove" && ph.Status == photoApproved:
		ph.Status = photoRemoved
	default:
		return nil, fmt.Errorf("can't %s a photo that is %s", action, ph.Status)
	}
	r.changed()
	if ph.Status == photoRejected || ph.Status == photoRemoved {
		return []string{ph.ID}, nil
	}
	return nil, nil
}

// move puts a print where the host dropped it and brings it to the top.
func (r *room) move(id string, x, y float64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return errClosed
	}
	ph := r.findPhoto(id)
	if ph == nil || ph.Status != photoApproved {
		return errNotFound
	}
	clamp := func(v float64) float64 { return math.Round(min(max(v, 0), 1)*1000) / 1000 }
	ph.X, ph.Y = clamp(x), clamp(y)
	r.NextZ++
	ph.Z = r.NextZ
	r.changed()
	return nil
}

func validStyle(s string) bool { return s == "shuffle" || s == "tidy" }

// arrange lays out every print on the desk.
//   - tidy: a neat grid in arrival order, nearly straight.
//   - shuffle: spread evenly over the desk at fresh angles, in a new stacking order,
//     so buried prints come back to the top.
//
// x and y are fractions of the free space (0 = left/top edge, 1 = right/bottom edge), so
// evenly spaced fractions give evenly spaced prints on any screen.
func (r *room) arrange(style string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return errClosed
	}
	if !validStyle(style) {
		return fmt.Errorf("unknown arrangement %q", style)
	}
	d := r.deskLocked(0)
	n := len(d)
	r.Arranged = time.Now().UTC()
	if n == 0 {
		return nil
	}
	// a widescreen desk holding roughly 4:3 prints
	cols := int(math.Ceil(math.Sqrt(float64(n) * 1.33)))
	cols = min(cols, n)
	rows := (n + cols - 1) / cols
	at := func(i, count int) float64 {
		if count == 1 {
			return 0.5
		}
		return float64(i) / float64(count-1)
	}
	round := func(v float64) float64 { return math.Round(v*1000) / 1000 }
	if style == "shuffle" {
		rand.Shuffle(n, func(i, j int) { d[i], d[j] = d[j], d[i] })
	}
	for i, ph := range d {
		c, rw := i%cols, i/cols
		inRow := cols
		if rw == rows-1 && n%cols != 0 {
			inRow = n % cols // centre a short last row
		}
		x, y := at(c, inRow), at(rw, rows)
		if inRow < cols && cols > 1 {
			x = (float64(c) + float64(cols-inRow)/2) / float64(cols-1)
		}
		if style == "tidy" {
			ph.Rot = math.Round((rand.Float64()*4-2)*10) / 10
		} else {
			// jitter within the cell so it looks thrown, not gridded
			jx, jy := 0.5/float64(max(cols-1, 1)), 0.5/float64(max(rows-1, 1))
			x += (rand.Float64()*2 - 1) * jx * 0.8
			y += (rand.Float64()*2 - 1) * jy * 0.8
			ph.Rot = math.Round((rand.Float64()*30-15)*10) / 10
		}
		ph.X, ph.Y = round(min(max(x, 0), 1)), round(min(max(y, 0), 1))
		r.NextZ++
		ph.Z = r.NextZ
	}
	r.changed()
	return nil
}

// arrangeDue reports whether the automatic re-arrange should run now.
func (r *room) arrangeDue(now time.Time) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.Settings.AutoArrange <= 0 {
		return "", false
	}
	style := r.Settings.AutoStyle
	if !validStyle(style) {
		style = "shuffle"
	}
	return style, now.Sub(r.Arranged) >= time.Duration(r.Settings.AutoArrange)*time.Minute
}

func (r *room) approveAll() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, ph := range r.Photos { // oldest first, so they stack in the order they arrived
		if ph.Status == photoPending {
			r.approveLocked(ph)
			n++
		}
	}
	r.changed()
	return n
}

func (r *room) clearDesk() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var ids []string
	for _, ph := range r.Photos {
		if ph.Status == photoApproved {
			ph.Status = photoRemoved
			ids = append(ids, ph.ID)
		}
	}
	r.changed()
	return ids
}

// deleteFiles removes stored images for photos that were rejected or taken off the desk.
func (r *room) deleteFiles(ids []string) {
	for _, id := range ids {
		os.Remove(filepath.Join(r.dir, id+".jpg"))
		os.Remove(filepath.Join(r.dir, id+"_m.jpg"))
	}
}

type exportItem struct {
	ID, Name string
	Created  time.Time
}

func (r *room) exportList() []exportItem {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []exportItem
	for _, ph := range r.deskLocked(0) {
		out = append(out, exportItem{ID: ph.ID, Name: r.nameFor(ph, true), Created: ph.Created})
	}
	return out
}

// ---- snapshots sent to the browser ----

type deskItem struct {
	ID      string  `json:"id"`
	Name    string  `json:"name,omitempty"`
	Caption string  `json:"caption,omitempty"`
	W       int     `json:"w"`
	H       int     `json:"h"`
	X       float64 `json:"x"`
	Y       float64 `json:"y"`
	Rot     float64 `json:"rot"`
	Z       int     `json:"z"`
}

type personView struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Anonymous bool      `json:"anonymous"`
	Status    string    `json:"status"`
	Joined    time.Time `json:"joined"`
	Sent      int       `json:"sent"`
	OnDesk    int       `json:"onDesk"`
}

type pendingView struct {
	ID      string    `json:"id"`
	PID     string    `json:"pid"`
	Name    string    `json:"name"`
	Caption string    `json:"caption,omitempty"`
	W       int       `json:"w"`
	H       int       `json:"h"`
	Created time.Time `json:"created"`
}

type myPhoto struct {
	ID      string    `json:"id"`
	Caption string    `json:"caption,omitempty"`
	Status  string    `json:"status"`
	Created time.Time `json:"created"`
}

type counts struct {
	People  int `json:"people"`
	Waiting int `json:"waiting"`
	Pending int `json:"pending"`
	Desk    int `json:"desk"`
}

type snapshot struct {
	Role       string        `json:"role"`
	Version    int64         `json:"version"`
	Code       string        `json:"code"`
	Title      string        `json:"title"`
	Settings   settings      `json:"settings"`
	JoinURL    string        `json:"joinUrl"`
	Desk       []deskItem    `json:"desk"`
	Counts     counts        `json:"counts"`
	DisplayKey string        `json:"displayKey,omitempty"`
	People     []personView  `json:"people,omitempty"`
	Pending    []pendingView `json:"pending,omitempty"`
	Me         *personView   `json:"me,omitempty"`
	Mine       []myPhoto     `json:"mine,omitempty"`
}

const deskLimit = 150 // most recent prints sent to displays and guests

// deskLocked returns approved photos bottom-to-top, keeping only the newest limit (0 = all).
func (r *room) deskLocked(limit int) []*photo {
	var d []*photo
	for _, ph := range r.Photos {
		if ph.Status == photoApproved {
			d = append(d, ph)
		}
	}
	sort.Slice(d, func(i, j int) bool { return d[i].Z < d[j].Z })
	if limit > 0 && len(d) > limit {
		d = d[len(d)-limit:]
	}
	return d
}

// nameFor is the name printed on a photo: blank for anonymous guests unless forHost.
func (r *room) nameFor(ph *photo, forHost bool) string {
	if ph.PID == hostPID {
		if forHost {
			return "Host"
		}
		return ""
	}
	p := r.byID[ph.PID]
	if p == nil || (p.Anonymous && !forHost) {
		return ""
	}
	return p.Name
}

func (r *room) personViewLocked(p *participant) personView {
	v := personView{ID: p.ID, Name: p.Name, Anonymous: p.Anonymous, Status: p.Status, Joined: p.Joined}
	for _, ph := range r.Photos {
		if ph.PID == p.ID {
			v.Sent++
			if ph.Status == photoApproved {
				v.OnDesk++
			}
		}
	}
	return v
}

// snapshot is everything one viewer needs to draw their page. closed reports an ended session.
func (r *room) snapshot(ro role, me *participant, joinURL string) (s snapshot, closed bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return s, true
	}
	s = snapshot{Role: ro.String(), Version: r.version, Code: r.Code, Title: r.Title, Settings: r.Settings, JoinURL: joinURL, Desk: []deskItem{}}
	for _, p := range r.People {
		switch p.Status {
		case statusAdmitted:
			s.Counts.People++
		case statusWaiting:
			s.Counts.Waiting++
		}
	}
	all := r.deskLocked(0)
	s.Counts.Desk = len(all)
	desk := all
	if ro != roleHost && len(desk) > deskLimit {
		desk = desk[len(desk)-deskLimit:]
	}
	for _, ph := range desk {
		s.Desk = append(s.Desk, deskItem{ID: ph.ID, Name: r.nameFor(ph, false), Caption: ph.Caption, W: ph.W, H: ph.H, X: ph.X, Y: ph.Y, Rot: ph.Rot, Z: ph.Z})
	}
	for _, ph := range r.Photos {
		if ph.Status == photoPending {
			s.Counts.Pending++
		}
	}
	switch ro {
	case roleHost:
		s.DisplayKey = r.DisplayKey
		s.People = []personView{}
		for _, p := range r.People {
			s.People = append(s.People, r.personViewLocked(p))
		}
		s.Pending = []pendingView{}
		for _, ph := range r.Photos {
			if ph.Status == photoPending {
				s.Pending = append(s.Pending, pendingView{ID: ph.ID, PID: ph.PID, Name: r.nameFor(ph, true), Caption: ph.Caption, W: ph.W, H: ph.H, Created: ph.Created})
			}
		}
	case roleParticipant:
		v := r.personViewLocked(me)
		s.Me = &v
		s.Mine = []myPhoto{}
		for _, ph := range r.Photos {
			if ph.PID == me.ID {
				s.Mine = append(s.Mine, myPhoto{ID: ph.ID, Caption: ph.Caption, Status: ph.Status, Created: ph.Created})
			}
		}
	}
	return s, false
}
