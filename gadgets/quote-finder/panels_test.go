package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
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
	if res.JobID != "j_9" || res.URL != "/panel.html?job=j_9" || res.ToolsURL != "http://pc:8103/jobs/j_9" || res.Lines != 3 {
		t.Fatalf("result %+v", res)
	}
	// the fetch is generous (3 s before, 5 s after) so the speech can be found even if the subtitles are off
	if !strings.Contains(clipQuery, "start=247.000") || !strings.Contains(clipQuery, "end=262.000") || !strings.Contains(clipQuery, "American+Dad") {
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
	if !strings.Contains(clipQuery, "start=247.000") || !strings.Contains(clipQuery, "end=262.000") ||
		clipBody["start"] != 0.0 || clipBody["end"] != 9.1 || clipBody["delete_input"] != true || len(clipBody["subtitles"].([]any)) != 3 {
		t.Fatalf("clip query %s body %v", clipQuery, clipBody)
	}
	// video-tools gets the lines in the fetched stretch to line the cut up with the speech
	al, _ := clipBody["align"].(map[string]any)
	if al == nil || len(al["lines"].([]any)) != 3 || al["first"] != 0.0 || al["last"] != 2.0 {
		t.Fatalf("align %v", clipBody["align"])
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

func TestPanelTimes(t *testing.T) {
	ls := []givenLine{{"a", 1, 2}, {"b", 2, 5}, {"c", 5, 6}, {"d", 7, 8}, {"e", 8, 8.5}, {"f", 9, 12}}
	if panelTimes(ls, 6) != nil || panelTimes(ls, 0) != nil || panelTimes(nil, 3) != nil {
		t.Fatal("one per line should be left to video-tools (nil)")
	}
	// two panels: lines a-c and d-f, each in the middle of its longest line (b, f)
	if got := panelTimes(ls, 2); !slices.Equal(got, []float64{3.5, 10.5}) {
		t.Fatalf("2 panels: %v", got)
	}
	// one panel: the longest line (b and f tie at 3 s; the earlier wins)
	if got := panelTimes(ls, 1); !slices.Equal(got, []float64{3.5}) {
		t.Fatalf("1 panel: %v", got)
	}
	// more panels than lines: every line keeps a frame, the extras fill the longest stretches
	two := []givenLine{{"a", 0, 2}, {"b", 10, 12}}
	got := panelTimes(two, 4)
	if len(got) != 4 || !slices.IsSorted(got) || got[0] != 1 || got[len(got)-1] != 11 {
		t.Fatalf("4 panels from 2 lines: %v", got)
	}
	if !slices.Contains(got, 6) { // the gap between the lines is split first
		t.Fatalf("expected a frame in the middle of the gap: %v", got)
	}
	for _, n := range []int{7, 12, 24} {
		got := panelTimes(ls, n)
		if len(got) != n || !slices.IsSorted(got) || got[0] < 1 || got[n-1] > 12 {
			t.Fatalf("%d panels: %v", n, got)
		}
	}
}

func TestRetakePanels(t *testing.T) {
	var sent struct {
		FileID string      `json:"file_id"`
		Lines  []givenLine `json:"lines"`
		Times  []float64   `json:"times"`
	}
	tools := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/jobs/j_abc123":
			io.WriteString(w, `{"id":"j_abc123","kind":"panel","title":"Show S01E02 4m10s.mkv","inputs":[{"id":"f_1"}],
				"params":{"lines":[{"text":"one","start":1,"end":2},{"text":"two","start":3,"end":5},{"text":"three","start":6,"end":7}]}}`)
		case r.Method == "GET" && r.URL.Path == "/api/jobs/j_join99":
			io.WriteString(w, `{"id":"j_join99","kind":"join","inputs":[{"id":"f_2"}]}`)
		case r.Method == "POST" && r.URL.Path == "/api/panel":
			json.NewDecoder(r.Body).Decode(&sent)
			w.WriteHeader(202)
			io.WriteString(w, `{"id":"j_new456","status":"queued"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer tools.Close()
	a := &app{panels: panelCfg{clipURL: "http://unused", videoTools: tools.URL}}

	res, err := a.retakePanels(context.Background(), "j_abc123", 2)
	if err != nil {
		t.Fatal(err)
	}
	if res.JobID != "j_new456" || res.URL != "/panel.html?job=j_new456" || res.Lines != 3 {
		t.Fatalf("result %+v", res)
	}
	if sent.FileID != "f_1" || len(sent.Lines) != 3 || sent.Lines[1].Text != "two" || len(sent.Times) != 2 {
		t.Fatalf("sent %+v", sent)
	}
	// the same count as lines goes back to video-tools' own choice (no times)
	sent.Times = nil
	if _, err := a.retakePanels(context.Background(), "j_abc123", 3); err != nil || sent.Times != nil {
		t.Fatalf("3 panels: err %v, times %v", err, sent.Times)
	}
	for _, n := range []int{0, 25} {
		if _, err := a.retakePanels(context.Background(), "j_abc123", n); err == nil {
			t.Fatalf("%d panels should be refused", n)
		}
	}
	if _, err := a.retakePanels(context.Background(), "j_join99", 2); err == nil {
		t.Fatal("a join job can't be re-taken")
	}
}
