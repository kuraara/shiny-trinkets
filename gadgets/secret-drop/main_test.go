package main

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// a 1x1 PNG
var png = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\rIDATx\x9cc\xf8\x0f\x00\x00\x01\x01\x00\x05\x18\xd8N\x00\x00\x00\x00IEND\xaeB`\x82")

func do(t *testing.T, h http.Handler, method, path, ctype string, body []byte) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func upload(t *testing.T, files map[string][]byte) (string, []byte) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for name, b := range files {
		w, _ := mw.CreateFormFile("image", name)
		w.Write(b)
	}
	mw.Close()
	return mw.FormDataContentType(), buf.Bytes()
}

func TestSaveListDelete(t *testing.T) {
	dir := t.TempDir()
	h := withAuth("tok", routes(dir))
	auth := func(method, path, ctype string, body []byte) (int, map[string]any) {
		req := httptest.NewRequest(method, path, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer tok")
		if ctype != "" {
			req.Header.Set("Content-Type", ctype)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var out map[string]any
		json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	if code, _ := do(t, h, "GET", "/api/secrets", "", nil); code != 401 {
		t.Fatalf("no token: got %d, want 401", code)
	}
	if code, out := auth("PUT", "/api/secrets/hydrawise", "application/json", []byte(`{"text":"USERNAME=me@example.com\r\nPASSWORD=p=w\nexport API_KEY=abc\n"}`)); code != 200 {
		t.Fatalf("save: %d %v", code, out)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "hydrawise.env"))
	if string(b) != "USERNAME=me@example.com\nPASSWORD=p=w\nexport API_KEY=abc\n" {
		t.Fatalf("file = %q", b)
	}
	if fi, _ := os.Stat(filepath.Join(dir, "hydrawise.env")); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", fi.Mode().Perm())
	}
	ctype, body := upload(t, map[string][]byte{"shot.png": png, "photo.HEIC": []byte("not sniffable")})
	if code, out := auth("POST", "/api/secrets/hydrawise/images", ctype, body); code != 200 || len(out["saved"].([]any)) != 2 {
		t.Fatalf("images: %d %v", code, out)
	}
	ctype, body = upload(t, map[string][]byte{"notes.txt": []byte("hello")})
	if code, _ := auth("POST", "/api/secrets/hydrawise/images", ctype, body); code != 400 {
		t.Fatalf("a text file as a picture: got %d, want 400", code)
	}
	code, out := auth("GET", "/api/secrets", "", nil)
	list := out["secrets"].([]any)
	if code != 200 || len(list) != 1 {
		t.Fatalf("list: %d %v", code, out)
	}
	e := list[0].(map[string]any)
	if e["images"].(float64) != 2 || strings.Join(toStrings(e["keys"]), ",") != "USERNAME,PASSWORD,API_KEY" {
		t.Fatalf("entry = %v", e)
	}
	if strings.Contains(strings.ToLower(jsonString(out)), "abc") || strings.Contains(jsonString(out), "p=w") {
		t.Fatalf("the listing leaks a value: %v", out)
	}
	if code, _ := auth("DELETE", "/api/secrets/hydrawise", "", nil); code != 200 {
		t.Fatalf("delete: %d", code)
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Fatalf("left behind: %v", left)
	}
}

func TestRefusesBadNames(t *testing.T) {
	dir := t.TempDir()
	h := routes(dir)
	for _, p := range []string{"/api/secrets/..%2Fescape", "/api/secrets/Upper", "/api/secrets/-x", "/api/secrets/a.b"} {
		if code, _ := do(t, h, "PUT", p, "application/json", []byte(`{"text":"A=1"}`)); code == 200 {
			t.Errorf("%s was accepted", p)
		}
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Fatalf("wrote %v", left)
	}
}

func toStrings(v any) []string {
	out := []string{}
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

func jsonString(v any) string { b, _ := json.Marshal(v); return string(b) }
