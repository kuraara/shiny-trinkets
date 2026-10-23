package main

import (
	"archive/zip"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
)

type app struct {
	cfg   config
	store *store
	web   fs.FS
}

func (a *app) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", a.page("index.html"))
	mux.HandleFunc("GET /j/{code}", a.page("join.html"))
	mux.HandleFunc("GET /h/{code}", a.page("host.html"))
	mux.HandleFunc("GET /s/{code}", a.page("screen.html"))
	mux.Handle("GET /static/", noCache(http.FileServerFS(a.web)))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ok\n") })

	mux.HandleFunc("GET /api/config", a.apiConfig)
	mux.HandleFunc("POST /api/rooms", a.createRoom)
	mux.HandleFunc("GET /api/rooms/{code}", a.roomInfo)
	mux.HandleFunc("DELETE /api/rooms/{code}", a.endRoom)
	mux.HandleFunc("GET /api/rooms/{code}/qr.svg", a.qr)
	mux.HandleFunc("POST /api/rooms/{code}/join", a.join)
	mux.HandleFunc("GET /api/rooms/{code}/state", a.state)
	mux.HandleFunc("GET /api/rooms/{code}/events", a.events)
	mux.HandleFunc("POST /api/rooms/{code}/settings", a.settings)
	mux.HandleFunc("POST /api/rooms/{code}/photos", a.upload)
	mux.HandleFunc("GET /api/rooms/{code}/photos/{file}", a.photoFile)
	mux.HandleFunc("POST /api/rooms/{code}/photos/{id}/{action}", a.photoAction)
	mux.HandleFunc("POST /api/rooms/{code}/approve-all", a.approveAll)
	mux.HandleFunc("POST /api/rooms/{code}/clear", a.clearDesk)
	mux.HandleFunc("POST /api/rooms/{code}/people/{pid}/{action}", a.personAction)
	mux.HandleFunc("POST /api/rooms/{code}/admit-all", a.admitAll)
	mux.HandleFunc("GET /api/rooms/{code}/export.zip", a.export)
	return headers(mux)
}

func headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func noCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		next.ServeHTTP(w, r)
	})
}

func (a *app) page(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := fs.ReadFile(a.web, name)
		if err != nil {
			http.Error(w, "missing page", http.StatusInternalServerError)
			return
		}
		h := w.Header()
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Cache-Control", "no-cache")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob:; style-src 'self' 'unsafe-inline'; connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'self'")
		w.Write(b)
	}
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// failErr maps model errors to HTTP statuses.
func failErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errNotFound):
		fail(w, http.StatusNotFound, "not found")
	case errors.Is(err, errClosed):
		fail(w, http.StatusGone, err.Error())
	case errors.Is(err, errNameRequired), errors.Is(err, errUnsupported), errors.Is(err, errHuge):
		fail(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, errTooManyPhotos), errors.Is(err, errTooManyQueued):
		fail(w, http.StatusTooManyRequests, err.Error())
	case errors.Is(err, errNotAdmitted), errors.Is(err, errRoomFull), errors.Is(err, errDeskFull), errors.Is(err, errTooManyRooms):
		fail(w, http.StatusForbidden, err.Error())
	default:
		fail(w, http.StatusConflict, err.Error())
	}
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil && !errors.Is(err, io.EOF) {
		fail(w, http.StatusBadRequest, "bad request body")
		return false
	}
	return true
}

func keyOf(r *http.Request) string {
	if k := r.Header.Get("X-Key"); k != "" {
		return k
	}
	return r.URL.Query().Get("k")
}

// room finds the session in the URL or writes a 404.
func (a *app) room(w http.ResponseWriter, r *http.Request) *room {
	rm := a.store.get(r.PathValue("code"))
	if rm == nil {
		fail(w, http.StatusNotFound, "This session doesn't exist or has ended.")
	}
	return rm
}

// hostRoom is room() plus a check that the caller holds the host key.
func (a *app) hostRoom(w http.ResponseWriter, r *http.Request) *room {
	rm := a.room(w, r)
	if rm == nil {
		return nil
	}
	if ro, _ := rm.auth(keyOf(r)); ro != roleHost {
		fail(w, http.StatusForbidden, "Only the host can do that.")
		return nil
	}
	return rm
}

func (a *app) baseURL(r *http.Request) string {
	if a.cfg.PublicURL != "" {
		return a.cfg.PublicURL
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	host := r.Host
	if fh := r.Header.Get("X-Forwarded-Host"); fh != "" {
		host = strings.TrimSpace(strings.Split(fh, ",")[0])
	}
	return scheme + "://" + host
}

func (a *app) joinURL(r *http.Request, code string) string { return a.baseURL(r) + "/j/" + code }

// cleanText trims, drops control characters, squashes whitespace and caps the length.
func cleanText(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if rs := []rune(s); len(rs) > max {
		s = strings.TrimSpace(string(rs[:max]))
	}
	return s
}

// ---- handlers ----

func (a *app) apiConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"pinRequired": a.cfg.CreatePIN != ""})
}

func (a *app) createRoom(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Title               string `json:"title"`
		RequireNames        bool   `json:"requireNames"`
		ApproveParticipants bool   `json:"approveParticipants"`
		PIN                 string `json:"pin"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if a.cfg.CreatePIN != "" && subtle.ConstantTimeCompare([]byte(in.PIN), []byte(a.cfg.CreatePIN)) != 1 {
		fail(w, http.StatusForbidden, "That PIN isn't right.")
		return
	}
	title := cleanText(in.Title, 60)
	if title == "" {
		title = "Photo Drop"
	}
	rm, err := a.store.create(title, settings{RequireNames: in.RequireNames, ApproveParticipants: in.ApproveParticipants, ShowQR: true})
	if err != nil {
		failErr(w, err)
		return
	}
	log.Printf("session %s started (%q)", rm.Code, title)
	writeJSON(w, http.StatusCreated, map[string]string{"code": rm.Code, "hostKey": rm.HostKey, "displayKey": rm.DisplayKey})
}

func (a *app) roomInfo(w http.ResponseWriter, r *http.Request) {
	rm := a.room(w, r)
	if rm == nil {
		return
	}
	title, set := rm.info()
	writeJSON(w, http.StatusOK, map[string]any{
		"code": rm.Code, "title": title, "joinUrl": a.joinURL(r, rm.Code),
		"requireNames": set.RequireNames, "approveParticipants": set.ApproveParticipants,
	})
}

func (a *app) endRoom(w http.ResponseWriter, r *http.Request) {
	rm := a.hostRoom(w, r)
	if rm == nil {
		return
	}
	a.store.end(rm)
	log.Printf("session %s ended by the host", rm.Code)
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) qr(w http.ResponseWriter, r *http.Request) {
	rm := a.room(w, r)
	if rm == nil {
		return
	}
	svg, err := qrSVG(a.joinURL(r, rm.Code))
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(svg)
}

func (a *app) join(w http.ResponseWriter, r *http.Request) {
	rm := a.room(w, r)
	if rm == nil {
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	p, err := rm.join(cleanText(in.Name, 40), a.cfg.MaxPeople)
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": p.ID, "token": p.Token, "name": p.Name, "status": p.Status})
}

func (a *app) viewer(w http.ResponseWriter, r *http.Request) (*room, role, *participant) {
	rm := a.room(w, r)
	if rm == nil {
		return nil, roleNone, nil
	}
	ro, p := rm.auth(keyOf(r))
	if ro == roleNone {
		fail(w, http.StatusForbidden, "This link is missing its key. Open it from the page that gave it to you.")
		return nil, roleNone, nil
	}
	return rm, ro, p
}

func (a *app) state(w http.ResponseWriter, r *http.Request) {
	rm, ro, p := a.viewer(w, r)
	if rm == nil {
		return
	}
	s, closed := rm.snapshot(ro, p, a.joinURL(r, rm.Code))
	if closed {
		fail(w, http.StatusGone, errClosed.Error())
		return
	}
	writeJSON(w, http.StatusOK, s)
}

// events streams a fresh snapshot (Server-Sent Events) whenever the session changes.
func (a *app) events(w http.ResponseWriter, r *http.Request) {
	rm, ro, p := a.viewer(w, r)
	if rm == nil {
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no") // tell nginx not to buffer the stream
	rc := http.NewResponseController(w)
	sub := rm.subscribe()
	defer rm.unsubscribe(sub)
	join := a.joinURL(r, rm.Code)

	send := func() bool {
		s, closed := rm.snapshot(ro, p, join)
		if closed {
			io.WriteString(w, "event: closed\ndata: {}\n\n")
			rc.Flush()
			return false
		}
		b, err := json.Marshal(s)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "event: state\ndata: %s\n\n", b); err != nil {
			return false
		}
		return rc.Flush() == nil
	}

	io.WriteString(w, "retry: 3000\n\n")
	if !send() {
		return
	}
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-a.store.quit:
			return
		case <-sub.ch:
			if !send() {
				return
			}
		case <-ping.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil || rc.Flush() != nil {
				return
			}
		}
	}
}

func (a *app) settings(w http.ResponseWriter, r *http.Request) {
	rm := a.hostRoom(w, r)
	if rm == nil {
		return
	}
	var p settingsPatch
	if !readJSON(w, r, &p) {
		return
	}
	if err := rm.update(p); err != nil {
		failErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) upload(w http.ResponseWriter, r *http.Request) {
	rm, ro, p := a.viewer(w, r)
	if rm == nil {
		return
	}
	host := ro == roleHost
	if !host && ro != roleParticipant {
		fail(w, http.StatusForbidden, "Only guests and the host can add photos.")
		return
	}
	if err := rm.canUpload(p, host, a.cfg); err != nil {
		failErr(w, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, a.cfg.MaxUploadBytes)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			fail(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("That photo is bigger than %d MB.", a.cfg.MaxUploadBytes>>20))
			return
		}
		fail(w, http.StatusBadRequest, "upload didn't arrive in one piece")
		return
	}
	defer r.MultipartForm.RemoveAll()
	f, _, err := r.FormFile("photo")
	if err != nil {
		fail(w, http.StatusBadRequest, "no photo in the upload")
		return
	}
	data, err := io.ReadAll(f)
	f.Close()
	if err != nil {
		fail(w, http.StatusBadRequest, "upload didn't arrive in one piece")
		return
	}
	caption := cleanText(r.FormValue("caption"), 80)

	a.store.imgSem <- struct{}{}
	full, medium, pw, ph, err := processImage(data)
	<-a.store.imgSem
	if err != nil {
		failErr(w, err)
		return
	}
	id := newID()
	if err := os.WriteFile(filepath.Join(rm.dir, id+".jpg"), full, 0o644); err != nil {
		fail(w, http.StatusInternalServerError, "couldn't save the photo")
		return
	}
	if err := os.WriteFile(filepath.Join(rm.dir, id+"_m.jpg"), medium, 0o644); err != nil {
		rm.deleteFiles([]string{id})
		fail(w, http.StatusInternalServerError, "couldn't save the photo")
		return
	}
	photo, err := rm.addPhoto(p, host, id, caption, pw, ph, a.cfg)
	if err != nil {
		rm.deleteFiles([]string{id})
		failErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": photo.ID, "status": photo.Status})
}

var photoFileRe = regexp.MustCompile(`^([a-z2-7]{26})(_m)?\.jpg$`)

// photoFile serves an image. Photo ids are long random strings that only the host, the
// sender and (once approved) the people watching the desk ever see.
func (a *app) photoFile(w http.ResponseWriter, r *http.Request) {
	rm := a.room(w, r)
	if rm == nil {
		return
	}
	m := photoFileRe.FindStringSubmatch(r.PathValue("file"))
	if m == nil || !rm.photoVisible(m[1]) {
		fail(w, http.StatusNotFound, "not found")
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=86400, immutable")
	http.ServeFile(w, r, filepath.Join(rm.dir, m[0]))
}

func (a *app) photoAction(w http.ResponseWriter, r *http.Request) {
	rm := a.hostRoom(w, r)
	if rm == nil {
		return
	}
	gone, err := rm.photoAction(r.PathValue("id"), r.PathValue("action"))
	if err != nil {
		failErr(w, err)
		return
	}
	rm.deleteFiles(gone)
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) approveAll(w http.ResponseWriter, r *http.Request) {
	rm := a.hostRoom(w, r)
	if rm == nil {
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"approved": rm.approveAll()})
}

func (a *app) clearDesk(w http.ResponseWriter, r *http.Request) {
	rm := a.hostRoom(w, r)
	if rm == nil {
		return
	}
	rm.deleteFiles(rm.clearDesk())
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) personAction(w http.ResponseWriter, r *http.Request) {
	rm := a.hostRoom(w, r)
	if rm == nil {
		return
	}
	gone, err := rm.person(r.PathValue("pid"), r.PathValue("action"))
	if err != nil {
		failErr(w, err)
		return
	}
	rm.deleteFiles(gone)
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) admitAll(w http.ResponseWriter, r *http.Request) {
	rm := a.hostRoom(w, r)
	if rm == nil {
		return
	}
	rm.admitAll()
	w.WriteHeader(http.StatusNoContent)
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// export downloads every photo on the desk as a zip, oldest first.
func (a *app) export(w http.ResponseWriter, r *http.Request) {
	rm := a.hostRoom(w, r)
	if rm == nil {
		return
	}
	items := rm.exportList()
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="photo-drop-%s.zip"`, rm.Code))
	zw := zip.NewWriter(w)
	for i, it := range items {
		f, err := os.Open(filepath.Join(rm.dir, it.ID+".jpg"))
		if err != nil {
			continue // removed while we were zipping
		}
		slug := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(it.Name), "-"), "-")
		if slug == "" {
			slug = "photo"
		}
		zf, err := zw.CreateHeader(&zip.FileHeader{Name: fmt.Sprintf("%03d-%s.jpg", i+1, slug), Method: zip.Store, Modified: it.Created})
		if err == nil {
			_, err = io.Copy(zf, f)
		}
		f.Close()
		if err != nil {
			return
		}
	}
	zw.Close()
}
