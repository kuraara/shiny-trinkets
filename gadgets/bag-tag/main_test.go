package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHello(t *testing.T) {
	srv := httptest.NewServer(routes())
	defer srv.Close()
	for q, want := range map[string]string{"": "Hello, there!", "?name=Sam": "Hello, Sam!"} {
		r, err := http.Get(srv.URL + "/api/hello" + q)
		if err != nil {
			t.Fatal(err)
		}
		var out map[string]string
		json.NewDecoder(r.Body).Decode(&out)
		r.Body.Close()
		if out["message"] != want {
			t.Errorf("%q: got %q", q, out["message"])
		}
	}
	if r, _ := http.Get(srv.URL + "/"); r.StatusCode != 200 {
		t.Errorf("page: %d", r.StatusCode)
	}
}
