package main

// OpenSubtitles (api.opensubtitles.com) client: find English subtitles for a file by its
// OpenSubtitles hash (exact release, so the timing matches), falling back to show/season/
// episode or movie title. Needs an API key; downloads also need a (free) account, which
// allows about 20 downloads a day.

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var errQuota = errors.New("OpenSubtitles daily download limit reached")

type openSubs struct {
	key, user, pass string
	token           string
	base            string
	remaining       int // downloads left today; -1 = unknown
	resetAt         time.Time
	client          *http.Client
}

func newOpenSubs() *openSubs {
	k := os.Getenv("OPENSUBTITLES_API_KEY")
	if k == "" {
		return nil
	}
	return &openSubs{key: k, user: os.Getenv("OPENSUBTITLES_USERNAME"), pass: os.Getenv("OPENSUBTITLES_PASSWORD"),
		base: "https://api.opensubtitles.com/api/v1", remaining: -1, client: &http.Client{Timeout: 60 * time.Second}}
}

// usable reports whether a download could happen now.
func (o *openSubs) usable() bool {
	if o == nil {
		return false
	}
	if o.remaining == 0 && time.Now().Before(o.resetAt) {
		return false
	}
	return true
}

func (o *openSubs) do(method, p string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	u := p
	if !strings.HasPrefix(p, "http") {
		u = o.base + p
	}
	req, _ := http.NewRequest(method, u, rd)
	req.Header.Set("Api-Key", o.key)
	req.Header.Set("User-Agent", "QuoteFinder v1.0")
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if o.token != "" {
		req.Header.Set("Authorization", "Bearer "+o.token)
	}
	res, err := o.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if res.StatusCode == 406 || res.StatusCode == 429 && strings.Contains(string(b), "download") {
		return errQuota
	}
	if res.StatusCode >= 300 {
		return fmt.Errorf("OpenSubtitles %s %s: %d %.200s", method, p, res.StatusCode, b)
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

func (o *openSubs) login() error {
	if o.token != "" || o.user == "" {
		return nil
	}
	var r struct {
		Token   string `json:"token"`
		BaseURL string `json:"base_url"`
	}
	if err := o.do("POST", "/login", map[string]string{"username": o.user, "password": o.pass}, &r); err != nil {
		return err
	}
	o.token = r.Token
	if r.BaseURL != "" {
		o.base = "https://" + strings.TrimPrefix(strings.TrimPrefix(r.BaseURL, "https://"), "http://") + "/api/v1"
	}
	return nil
}

// osHash is the OpenSubtitles file hash: size plus 64-bit sums of the first and last 64 KB.
func osHash(p string) (string, error) {
	const chunk = 64 * 1024
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	size := st.Size()
	if size < chunk*2 {
		return "", errors.New("file too small to hash")
	}
	h := uint64(size)
	buf := make([]byte, chunk)
	for _, off := range []int64{0, size - chunk} {
		if _, err := f.ReadAt(buf, off); err != nil {
			return "", err
		}
		for i := 0; i < chunk; i += 8 {
			h += binary.LittleEndian.Uint64(buf[i:])
		}
	}
	return fmt.Sprintf("%016x", h), nil
}

type osResult struct {
	Attributes struct {
		Language       string `json:"language"`
		MoviehashMatch bool   `json:"moviehash_match"`
		Downloads      int    `json:"download_count"`
		Files          []struct {
			FileID int `json:"file_id"`
		} `json:"files"`
		Release string `json:"release"`
	} `json:"attributes"`
}

var (
	pSE   = regexp.MustCompile(`(?i)\bS(\d{1,2})[ ._-]?E(\d{1,3})`)
	pX    = regexp.MustCompile(`(?i)\b(\d{1,2})x(\d{1,3})\b`)
	pYear = regexp.MustCompile(`[(\[. _-]((?:19|20)\d\d)[)\]. _-]`)
)

// guess works out what to search for from the library path.
func guess(rel string) (query string, season, episode, year int) {
	parts := strings.Split(rel, "/")
	file := strings.TrimSuffix(parts[len(parts)-1], path.Ext(rel))
	top := strings.ToLower(parts[0])
	if (strings.Contains(top, "tv") || strings.Contains(top, "show")) && len(parts) >= 3 {
		query = parts[1]
		if m := pSE.FindStringSubmatch(file); m != nil {
			season, _ = strconv.Atoi(m[1])
			episode, _ = strconv.Atoi(m[2])
		} else if m := pX.FindStringSubmatch(file); m != nil {
			season, _ = strconv.Atoi(m[1])
			episode, _ = strconv.Atoi(m[2])
		}
		return
	}
	name := file
	if len(parts) >= 3 {
		name = parts[len(parts)-2]
	}
	if m := pYear.FindStringSubmatchIndex(" " + name + " "); m != nil {
		year, _ = strconv.Atoi((" " + name + " ")[m[2]:m[3]])
		name = name[:max(0, m[0]-1)]
	}
	query = strings.Join(strings.Fields(strings.NewReplacer(".", " ", "_", " ", "(", " ", ")", " ").Replace(name)), " ")
	return
}

// fetch finds and downloads English subtitles for one file. It returns the SRT and a note
// on where it came from.
func (o *openSubs) fetch(abs, rel string) (string, string, error) {
	if !o.usable() {
		return "", "", errQuota
	}
	if err := o.login(); err != nil {
		return "", "", err
	}
	var found []osResult
	hash, _ := osHash(abs)
	if hash != "" {
		var r struct{ Data []osResult }
		if err := o.do("GET", "/subtitles?languages=en&moviehash="+hash, nil, &r); err != nil {
			return "", "", err
		}
		for _, d := range r.Data {
			if d.Attributes.MoviehashMatch {
				found = append(found, d)
			}
		}
	}
	how := "exact file match"
	if len(found) == 0 {
		q, season, episode, year := guess(rel)
		v := url.Values{"languages": {"en"}, "query": {strings.ToLower(q)}}
		if season > 0 && episode > 0 {
			v.Set("season_number", strconv.Itoa(season))
			v.Set("episode_number", strconv.Itoa(episode))
			v.Set("type", "episode")
		} else if year > 0 {
			v.Set("year", strconv.Itoa(year))
			v.Set("type", "movie")
		}
		var r struct{ Data []osResult }
		if err := o.do("GET", "/subtitles?"+v.Encode(), nil, &r); err != nil {
			return "", "", err
		}
		found = r.Data
		how = "matched by title"
	}
	best := -1
	for i, d := range found {
		if len(d.Attributes.Files) == 0 {
			continue
		}
		if best < 0 || d.Attributes.Downloads > found[best].Attributes.Downloads {
			best = i
		}
	}
	if best < 0 {
		return "", "OpenSubtitles: not found", nil
	}
	var dl struct {
		Link      string `json:"link"`
		Remaining int    `json:"remaining"`
		ResetUTC  string `json:"reset_time_utc"`
	}
	if err := o.do("POST", "/download", map[string]int{"file_id": found[best].Attributes.Files[0].FileID}, &dl); err != nil {
		if errors.Is(err, errQuota) {
			o.remaining, o.resetAt = 0, time.Now().Add(6*time.Hour)
		}
		return "", "", err
	}
	o.remaining = dl.Remaining
	if t, err := time.Parse(time.RFC3339, dl.ResetUTC); err == nil {
		o.resetAt = t
	}
	res, err := o.client.Get(dl.Link)
	if err != nil {
		return "", "", err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if err != nil {
		return "", "", err
	}
	return string(b), fmt.Sprintf("OpenSubtitles (%s: %s)", how, found[best].Attributes.Release), nil
}
