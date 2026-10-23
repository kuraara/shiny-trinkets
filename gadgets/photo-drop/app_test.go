package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testConfig(dir string) config {
	return config{DataDir: dir, RoomTTL: time.Hour, MaxRooms: 5, MaxPhotos: 100, MaxPeople: 10,
		MaxPerPerson: 5, MaxPendingEach: 3, MaxUploadBytes: 10 << 20}
}

func newTestServer(t *testing.T, dir string) (*httptest.Server, *store) {
	t.Helper()
	st, err := openStore(testConfig(dir))
	if err != nil {
		t.Fatal(err)
	}
	web, _ := fs.Sub(webFiles, "web")
	srv := httptest.NewServer((&app{cfg: st.cfg, store: st, web: web}).routes())
	t.Cleanup(func() { st.shutdown(); srv.Close() })
	return srv, st
}

func call(t *testing.T, srv *httptest.Server, method, path, key string, body any) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, srv.URL+path, rd)
	if key != "" {
		req.Header.Set("X-Key", key)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func testJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 128, 255})
		}
	}
	var buf bytes.Buffer
	jpeg.Encode(&buf, img, nil)
	return buf.Bytes()
}

func upload(t *testing.T, srv *httptest.Server, code, key string, data []byte, caption string) (int, map[string]any) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("photo", "p.jpg")
	fw.Write(data)
	mw.WriteField("caption", caption)
	mw.Close()
	req, _ := http.NewRequest("POST", srv.URL+"/api/rooms/"+code+"/photos", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-Key", key)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func state(t *testing.T, srv *httptest.Server, code, key string) snapshot {
	t.Helper()
	req, _ := http.NewRequest("GET", srv.URL+"/api/rooms/"+code+"/state", nil)
	req.Header.Set("X-Key", key)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("state: %d", res.StatusCode)
	}
	var s snapshot
	json.NewDecoder(res.Body).Decode(&s)
	return s
}

func TestFullFlow(t *testing.T) {
	dir := t.TempDir()
	srv, _ := newTestServer(t, dir)

	st, room := call(t, srv, "POST", "/api/rooms", "", map[string]any{"title": "Party", "requireNames": true, "approveParticipants": true})
	if st != 201 {
		t.Fatalf("create: %d %v", st, room)
	}
	code, hostKey, dispKey := room["code"].(string), room["hostKey"].(string), room["displayKey"].(string)

	// Names are required.
	if st, _ := call(t, srv, "POST", "/api/rooms/"+code+"/join", "", map[string]any{"name": " "}); st != 400 {
		t.Fatalf("blank name should be refused, got %d", st)
	}
	st, guest := call(t, srv, "POST", "/api/rooms/"+code+"/join", "", map[string]any{"name": "Alex\x00 Smith"})
	if st != 201 || guest["status"] != "waiting" || guest["name"] != "Alex Smith" {
		t.Fatalf("join: %d %v", st, guest)
	}
	tok, pid := guest["token"].(string), guest["id"].(string)

	// Waiting guests can't upload.
	if st, _ := upload(t, srv, code, tok, testJPEG(t, 40, 30), ""); st != 403 {
		t.Fatalf("waiting guest upload: want 403, got %d", st)
	}
	// Only the host can admit.
	if st, _ := call(t, srv, "POST", "/api/rooms/"+code+"/people/"+pid+"/admit", tok, nil); st != 403 {
		t.Fatalf("guest admitting themselves: want 403, got %d", st)
	}
	if st, _ := call(t, srv, "POST", "/api/rooms/"+code+"/people/"+pid+"/admit", hostKey, nil); st != 204 {
		t.Fatalf("admit: %d", st)
	}

	st, up := upload(t, srv, code, tok, testJPEG(t, 3000, 1000), "cake!")
	if st != 201 || up["status"] != "pending" {
		t.Fatalf("upload: %d %v", st, up)
	}
	id := up["id"].(string)

	// Pending photo is invisible to the display, visible to the host.
	if s := state(t, srv, code, dispKey); len(s.Desk) != 0 || s.Role != "display" {
		t.Fatalf("display sees pending photo: %+v", s)
	}
	hs := state(t, srv, code, hostKey)
	if len(hs.Pending) != 1 || hs.Pending[0].Name != "Alex Smith" || hs.Pending[0].W != 2048 || hs.Pending[0].H != 683 {
		t.Fatalf("host pending: %+v", hs.Pending)
	}
	if res, _ := http.Get(srv.URL + "/api/rooms/" + code + "/photos/" + id + "_m.jpg"); res.StatusCode != 200 {
		t.Fatalf("medium file: %d", res.StatusCode)
	}

	if st, _ := call(t, srv, "POST", "/api/rooms/"+code+"/photos/"+id+"/approve", hostKey, nil); st != 204 {
		t.Fatalf("approve: %d", st)
	}
	ds := state(t, srv, code, dispKey)
	if len(ds.Desk) != 1 || ds.Desk[0].Caption != "cake!" || ds.Desk[0].Name != "Alex Smith" {
		t.Fatalf("desk: %+v", ds.Desk)
	}
	if r := ds.Desk[0].Rot; r < -15 || r > 15 {
		t.Fatalf("rotation out of range: %v", r)
	}
	ps := state(t, srv, code, tok)
	if ps.Me == nil || ps.Me.OnDesk != 1 || len(ps.Mine) != 1 || ps.Mine[0].Status != "approved" || ps.DisplayKey != "" {
		t.Fatalf("guest view: %+v", ps)
	}

	// Rejected photos are deleted from disk.
	_, up2 := upload(t, srv, code, tok, testJPEG(t, 50, 50), "")
	id2 := up2["id"].(string)
	call(t, srv, "POST", "/api/rooms/"+code+"/photos/"+id2+"/reject", hostKey, nil)
	if _, err := os.Stat(filepath.Join(dir, "rooms", code, id2+".jpg")); !os.IsNotExist(err) {
		t.Fatalf("rejected file still on disk")
	}

	// Export has the one desk photo.
	res, _ := http.Get(srv.URL + "/api/rooms/" + code + "/export.zip?k=" + hostKey)
	zb, _ := io.ReadAll(res.Body)
	zr, err := zip.NewReader(bytes.NewReader(zb), int64(len(zb)))
	if err != nil || len(zr.File) != 1 || zr.File[0].Name != "001-alex-smith.jpg" {
		t.Fatalf("zip: %v %v", err, zr)
	}

	// Kick: queued photos go, further uploads refused.
	_, up3 := upload(t, srv, code, tok, testJPEG(t, 50, 50), "")
	call(t, srv, "POST", "/api/rooms/"+code+"/people/"+pid+"/kick", hostKey, nil)
	if s := state(t, srv, code, hostKey); len(s.Pending) != 0 {
		t.Fatalf("kicked guest's photo still pending")
	}
	if st, _ := upload(t, srv, code, tok, testJPEG(t, 50, 50), ""); st != 403 {
		t.Fatalf("kicked guest upload: %d", st)
	}
	_ = up3

	// End session: data gone, links dead.
	if st, _ := call(t, srv, "DELETE", "/api/rooms/"+code, hostKey, nil); st != 204 {
		t.Fatalf("end: %d", st)
	}
	if st, _ := call(t, srv, "GET", "/api/rooms/"+code, "", nil); st != 404 {
		t.Fatalf("ended room still there: %d", st)
	}
	if _, err := os.Stat(filepath.Join(dir, "rooms", code)); !os.IsNotExist(err) {
		t.Fatalf("room dir not deleted")
	}
}

func TestAnonymousAndLimits(t *testing.T) {
	srv, _ := newTestServer(t, t.TempDir())
	_, room := call(t, srv, "POST", "/api/rooms", "", map[string]any{})
	code, hostKey := room["code"].(string), room["hostKey"].(string)
	_, g := call(t, srv, "POST", "/api/rooms/"+code+"/join", "", map[string]any{})
	if g["status"] != "admitted" || g["name"] != "Guest 1" {
		t.Fatalf("anon join: %v", g)
	}
	tok := g["token"].(string)
	for i := 0; i < 3; i++ {
		if st, _ := upload(t, srv, code, tok, testJPEG(t, 20, 20), ""); st != 201 {
			t.Fatalf("upload %d: %d", i, st)
		}
	}
	if st, _ := upload(t, srv, code, tok, testJPEG(t, 20, 20), ""); st != 429 {
		t.Fatalf("4th queued upload should be refused, got %d", st)
	}
	call(t, srv, "POST", "/api/rooms/"+code+"/approve-all", hostKey, nil)
	s := state(t, srv, code, hostKey)
	if len(s.Desk) != 3 || s.Desk[0].Name != "" {
		t.Fatalf("anon desk: %+v", s.Desk)
	}
	if st, _ := upload(t, srv, code, "nonsense", testJPEG(t, 20, 20), ""); st != 403 {
		t.Fatalf("bad key upload: %d", st)
	}
	if st, _ := upload(t, srv, code, tok, []byte("not an image"), ""); st != 400 {
		t.Fatalf("junk upload: %d", st)
	}
	// Host uploads go straight onto the desk.
	if st, up := upload(t, srv, code, hostKey, testJPEG(t, 20, 20), ""); st != 201 || up["status"] != "approved" {
		t.Fatalf("host upload: %d %v", st, up)
	}
}

func TestTurningOffApprovalLetsEveryoneIn(t *testing.T) {
	srv, _ := newTestServer(t, t.TempDir())
	_, room := call(t, srv, "POST", "/api/rooms", "", map[string]any{"approveParticipants": true})
	code, hostKey := room["code"].(string), room["hostKey"].(string)
	_, g := call(t, srv, "POST", "/api/rooms/"+code+"/join", "", map[string]any{"name": "Sam"})
	call(t, srv, "POST", "/api/rooms/"+code+"/settings", hostKey, map[string]any{"approveParticipants": false})
	if s := state(t, srv, code, g["token"].(string)); s.Me.Status != "admitted" {
		t.Fatalf("still waiting: %s", s.Me.Status)
	}
}

func TestPersistence(t *testing.T) {
	dir := t.TempDir()
	srv, st := newTestServer(t, dir)
	_, room := call(t, srv, "POST", "/api/rooms", "", map[string]any{"title": "Keep me"})
	code := room["code"].(string)
	_, g := call(t, srv, "POST", "/api/rooms/"+code+"/join", "", map[string]any{"name": "Jo"})
	_, up := upload(t, srv, code, room["hostKey"].(string), testJPEG(t, 30, 20), "")
	st.saveDirty()

	st2, err := openStore(testConfig(dir))
	if err != nil {
		t.Fatal(err)
	}
	r := st2.get(code)
	if r == nil || r.Title != "Keep me" {
		t.Fatalf("room not reloaded")
	}
	if ro, p := r.auth(g["token"].(string)); ro != roleParticipant || p.Name != "Jo" {
		t.Fatalf("guest token lost after reload")
	}
	if !r.photoVisible(up["id"].(string)) {
		t.Fatalf("photo lost after reload")
	}
}

func TestEventsStream(t *testing.T) {
	srv, _ := newTestServer(t, t.TempDir())
	_, room := call(t, srv, "POST", "/api/rooms", "", map[string]any{})
	code, hostKey, dispKey := room["code"].(string), room["hostKey"].(string), room["displayKey"].(string)
	res, err := http.Get(srv.URL + "/api/rooms/" + code + "/events?k=" + dispKey)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	rd := bufio.NewReader(res.Body)
	next := func() snapshot {
		for {
			line, err := rd.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(line, "data: ") {
				var s snapshot
				json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &s)
				return s
			}
		}
	}
	if s := next(); len(s.Desk) != 0 {
		t.Fatalf("first snapshot: %+v", s)
	}
	upload(t, srv, code, hostKey, testJPEG(t, 20, 20), "hi")
	if s := next(); len(s.Desk) != 1 || s.Desk[0].Caption != "hi" {
		t.Fatalf("after upload: %+v", s.Desk)
	}
}

func TestOrientation(t *testing.T) {
	// Minimal EXIF APP1 segment, big-endian, orientation 6 (rotate 90° clockwise).
	exif := []byte("Exif\x00\x00MM\x00\x2a\x00\x00\x00\x08\x00\x01\x01\x12\x00\x03\x00\x00\x00\x01\x00\x06\x00\x00\x00\x00\x00\x00")
	seg := append([]byte{0xFF, 0xE1, 0, byte(len(exif) + 2)}, exif...)
	plain := testJPEG(t, 40, 20)
	withExif := append(append([]byte{0xFF, 0xD8}, seg...), plain[2:]...)
	if o := jpegOrientation(withExif); o != 6 {
		t.Fatalf("orientation = %d, want 6", o)
	}
	_, _, w, h, err := processImage(withExif)
	if err != nil || w != 20 || h != 40 {
		t.Fatalf("rotated size %dx%d err %v, want 20x40", w, h, err)
	}
	if o := jpegOrientation(plain); o != 1 {
		t.Fatalf("plain orientation = %d", o)
	}
}

func TestQR(t *testing.T) {
	svg, err := qrSVG("http://<pc>:8102/j/ABCDEF")
	if err != nil || !bytes.HasPrefix(svg, []byte("<svg")) {
		t.Fatalf("qr: %v", err)
	}
}
