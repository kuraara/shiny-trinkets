package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newTestServer(t *testing.T, dir string, c *clock) (*httptest.Server, *store) {
	t.Helper()
	s := newStore(dir, c.now)
	ts := httptest.NewServer(routes(s))
	t.Cleanup(ts.Close)
	return ts, s
}

func do(t *testing.T, method, url, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func TestPageIsServedUnchanged(t *testing.T) {
	ts, _ := newTestServer(t, "", &clock{time.Unix(1000, 0)})
	want, err := os.ReadFile("round-timer.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", "/round-timer.html"} {
		res, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		buf.ReadFrom(res.Body)
		res.Body.Close()
		if res.StatusCode != 200 || !bytes.Equal(buf.Bytes(), want) {
			t.Fatalf("%s: status %d, %d bytes (want 200, %d bytes identical to round-timer.html)", path, res.StatusCode, buf.Len(), len(want))
		}
		if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Fatalf("%s: content type %q", path, ct)
		}
	}
	if res, _ := http.Get(ts.URL + "/nope"); res.StatusCode != 404 {
		t.Fatalf("/nope: %d, want 404", res.StatusCode)
	}
	if res, _ := http.Get(ts.URL + "/healthz"); res.StatusCode != 200 {
		t.Fatalf("/healthz: %d", res.StatusCode)
	}
	if res, _ := http.Get(ts.URL + "/icon.svg"); res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/svg+xml" {
		t.Fatalf("/icon.svg: %d %q", res.StatusCode, res.Header.Get("Content-Type"))
	}
}

func TestStateRoundTrip(t *testing.T) {
	c := &clock{time.Unix(1_000_000, 0)}
	ts, _ := newTestServer(t, "", c)

	code, got := do(t, "GET", ts.URL+"/api/state", "")
	if code != 200 || got["rev"].(float64) != 0 || got["state"] != nil || got["now"].(float64) != 1_000_000_000 {
		t.Fatalf("fresh GET: %d %v", code, got)
	}

	code, got = do(t, "PUT", ts.URL+"/api/state", `{"rev":0,"state":{"v":1,"status":"running"}}`)
	if code != 200 || got["rev"].(float64) != 1 {
		t.Fatalf("first PUT: %d %v", code, got)
	}
	code, got = do(t, "GET", ts.URL+"/api/state", "")
	st, _ := got["state"].(map[string]any)
	if code != 200 || got["rev"].(float64) != 1 || st["status"] != "running" || got["updated"].(float64) != 1_000_000_000 {
		t.Fatalf("GET after PUT: %d %v", code, got)
	}

	// clearing
	code, got = do(t, "PUT", ts.URL+"/api/state", `{"rev":1,"state":null}`)
	if code != 200 || got["rev"].(float64) != 2 {
		t.Fatalf("clear: %d %v", code, got)
	}
	if _, got = do(t, "GET", ts.URL+"/api/state", ""); got["state"] != nil || got["rev"].(float64) != 2 {
		t.Fatalf("GET after clear: %v", got)
	}
}

func TestConflictReturnsCurrentState(t *testing.T) {
	ts, _ := newTestServer(t, "", &clock{time.Unix(5, 0)})
	do(t, "PUT", ts.URL+"/api/state", `{"rev":0,"state":{"who":"phone"}}`)
	code, got := do(t, "PUT", ts.URL+"/api/state", `{"rev":0,"state":{"who":"laptop"}}`)
	st, _ := got["state"].(map[string]any)
	if code != 409 || got["conflict"] != true || got["rev"].(float64) != 1 || st["who"] != "phone" {
		t.Fatalf("stale PUT: %d %v", code, got)
	}
	// a client that is ahead of the server (it was reset) is also told, with the server's rev
	code, got = do(t, "PUT", ts.URL+"/api/state", `{"rev":9,"state":{"who":"laptop"}}`)
	if code != 409 || got["rev"].(float64) != 1 {
		t.Fatalf("PUT ahead of the server: %d %v", code, got)
	}
}

func TestRejectsBadInput(t *testing.T) {
	ts, _ := newTestServer(t, "", &clock{time.Unix(5, 0)})
	cases := map[string]struct {
		body string
		want int
	}{
		"not json":       {`hello`, 400},
		"no rev":         {`{"state":{}}`, 400},
		"state an array": {`{"rev":0,"state":[1,2]}`, 400},
		"state a string": {`{"rev":0,"state":"x"}`, 400},
		"too big":        {`{"rev":0,"state":{"x":"` + strings.Repeat("a", maxBody) + `"}}`, 413},
	}
	for name, c := range cases {
		if code, _ := do(t, "PUT", ts.URL+"/api/state", c.body); code != c.want {
			t.Errorf("%s: %d, want %d", name, code, c.want)
		}
	}
	if _, got := do(t, "GET", ts.URL+"/api/state", ""); got["rev"].(float64) != 0 {
		t.Errorf("a rejected PUT changed the revision: %v", got)
	}
	if code, _ := do(t, "POST", ts.URL+"/api/state", `{}`); code != 405 {
		t.Errorf("POST: %d, want 405", code)
	}
}

func TestStateSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	c := &clock{time.Unix(2_000, 0)}
	ts, _ := newTestServer(t, dir, c)
	do(t, "PUT", ts.URL+"/api/state", `{"rev":0,"state":{"status":"paused","n":7}}`)
	ts.Close()

	ts2, _ := newTestServer(t, dir, c)
	code, got := do(t, "GET", ts2.URL+"/api/state", "")
	st, _ := got["state"].(map[string]any)
	if code != 200 || got["rev"].(float64) != 1 || st["n"].(float64) != 7 {
		t.Fatalf("after restart: %d %v", code, got)
	}
	// and a cleared session stays cleared
	do(t, "PUT", ts2.URL+"/api/state", `{"rev":1,"state":null}`)
	ts3, _ := newTestServer(t, dir, c)
	if _, got = do(t, "GET", ts3.URL+"/api/state", ""); got["state"] != nil || got["rev"].(float64) != 2 {
		t.Fatalf("cleared state came back: %v", got)
	}
}

func TestOldSessionsExpireButTheRevisionStays(t *testing.T) {
	c := &clock{time.Unix(10_000, 0)}
	ts, _ := newTestServer(t, "", c)
	do(t, "PUT", ts.URL+"/api/state", `{"rev":0,"state":{"a":1}}`)
	c.t = c.t.Add(stateTTL + time.Minute)
	code, got := do(t, "GET", ts.URL+"/api/state", "")
	if code != 200 || got["state"] != nil || got["rev"].(float64) != 1 {
		t.Fatalf("expired: %d %v", code, got)
	}
	if code, got = do(t, "PUT", ts.URL+"/api/state", `{"rev":1,"state":{"a":2}}`); code != 200 || got["rev"].(float64) != 2 {
		t.Fatalf("PUT after expiry: %d %v", code, got)
	}
}

func TestBrokenStateFileIsIgnored(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(dir+"/state.json", []byte("{not json"), 0o644)
	ts, _ := newTestServer(t, dir, &clock{time.Unix(1, 0)})
	if code, got := do(t, "GET", ts.URL+"/api/state", ""); code != 200 || got["state"] != nil || got["rev"].(float64) != 0 {
		t.Fatalf("broken file: %d %v", code, got)
	}
	if code, _ := do(t, "PUT", ts.URL+"/api/state", `{"rev":0,"state":{"ok":true}}`); code != 200 {
		t.Fatalf("could not write over a broken file: %d", code)
	}
}
