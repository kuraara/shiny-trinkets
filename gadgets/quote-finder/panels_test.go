package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestMakePanels(t *testing.T) {
	db, err := openDB(filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatal(err)
	}
	srt := "1\n00:04:10,000 --> 00:04:12,000\nMy fingers are still sticky.\n\n" +
		"2\n00:04:12,500 --> 00:04:14,000\nYou can suck on them if you want.\n\n" +
		"3\n00:04:15,000 --> 00:04:17,000\nWell, I'll be upstairs\n\n" +
		"4\n00:04:30,000 --> 00:04:31,000\nLater line.\n"
	db.put(ingestItem{Path: "TvShows/American Dad!/Season 8/American.Dad.S08E09.Stanny.Tendergrass.mkv", Status: "indexed", SRT: srt})

	var clipQuery string
	clips := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "bad token", 401)
			return
		}
		clipQuery = r.URL.RawQuery
		w.Header().Set("X-Clip-Start", "249.100") // keyframe before the first line
		w.Write([]byte(strings.Repeat("x", 4096)))
	}))
	defer clips.Close()
	var gotName string
	var gotSize int
	var panelBody struct {
		FileID string `json:"file_id"`
		Lines  []struct {
			Text       string
			Start, End float64
		} `json:"lines"`
	}
	var clipBody map[string]any
	tools := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/files":
			f, fh, err := r.FormFile("file")
			if err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			b, _ := io.ReadAll(f)
			gotName, gotSize = fh.Filename, len(b)
			w.WriteHeader(201)
			io.WriteString(w, `{"files":[{"id":"f_1"}]}`)
		case "/api/panel":
			json.NewDecoder(r.Body).Decode(&panelBody)
			w.WriteHeader(202)
			io.WriteString(w, `{"id":"j_9","status":"queued"}`)
		case "/api/clip":
			json.NewDecoder(r.Body).Decode(&clipBody)
			w.WriteHeader(202)
			io.WriteString(w, `{"id":"j_10","status":"queued"}`)
		}
	}))
	defer tools.Close()

	a := &app{db: db, token: "tok", panels: panelCfg{clipURL: clips.URL, videoTools: tools.URL, publicTools: "http://pc:8103"}}
	res, err := a.makePanels(context.Background(), 1, 255000, 250000) // reversed on purpose
	if err != nil {
		t.Fatal(err)
	}
	if res.JobID != "j_9" || res.URL != "http://pc:8103/jobs/j_9" || res.Lines != 3 {
		t.Fatalf("result %+v", res)
	}
	if !strings.Contains(clipQuery, "start=249.600") || !strings.Contains(clipQuery, "end=257.600") || !strings.Contains(clipQuery, "American+Dad") {
		t.Fatalf("clip query %s", clipQuery)
	}
	if gotName != "American Dad! S08E09 4m10s.mkv" || gotSize != 4096 {
		t.Fatalf("upload %q %d", gotName, gotSize)
	}
	if panelBody.FileID != "f_1" || len(panelBody.Lines) != 3 || panelBody.Lines[0].Start != 0.9 || panelBody.Lines[2].End != 7.9 ||
		panelBody.Lines[2].Text != "Well, I'll be upstairs" {
		t.Fatalf("panel body %+v", panelBody)
	}
	// a clip: a second either side, subtitles burned in, the excerpt deleted afterwards
	res, err = a.makeScene(context.Background(), 1, 250000, 255000, sceneOpts{Clip: true, Subtitles: true})
	if err != nil || res.JobID != "j_10" {
		t.Fatalf("clip %+v %v", res, err)
	}
	if !strings.Contains(clipQuery, "start=249.000") || !strings.Contains(clipQuery, "end=258.200") ||
		clipBody["start"] != 0.0 || clipBody["end"] != 9.1 || clipBody["delete_input"] != true || len(clipBody["subtitles"].([]any)) != 3 {
		t.Fatalf("clip query %s body %v", clipQuery, clipBody)
	}
	if _, err := a.makePanels(context.Background(), 1, 1000, 2000); err == nil {
		t.Fatal("empty range should fail")
	}

	// the same through MCP
	srv := httptest.NewServer(a.routes())
	defer srv.Close()
	call := func(body string) map[string]any {
		r, err := http.Post(srv.URL+"/mcp", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		var out map[string]any
		json.NewDecoder(r.Body).Decode(&out)
		return out
	}
	if tl := call(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`); len(tl["result"].(map[string]any)["tools"].([]any)) != 5 {
		t.Fatalf("tools %v", tl)
	}
	sr := call(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search_quotes","arguments":{"query":"upstairs"}}}`)
	txt := sr["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(txt, "media_id 1, at_ms 255000") {
		t.Fatalf("search text %s", txt)
	}
	mk := call(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"make_panel_script","arguments":{"media_id":1,"from_ms":250000,"to_ms":255000}}}`)
	if r := mk["result"].(map[string]any); r["isError"] == true || !strings.Contains(r["content"].([]any)[0].(map[string]any)["text"].(string), "j_9") {
		t.Fatalf("make via mcp %v", mk)
	}
	// browsers on other sites can't start jobs
	req, _ := http.NewRequest("POST", srv.URL+"/api/panels", strings.NewReader(`{"media":1,"from":250000,"to":255000}`))
	req.Header.Set("Origin", "http://evil.example")
	if r, _ := http.DefaultClient.Do(req); r.StatusCode != 403 {
		t.Fatalf("cross-origin %d", r.StatusCode)
	}
}
