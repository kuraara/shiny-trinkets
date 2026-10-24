package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestParsePath(t *testing.T) {
	cases := []struct {
		in   string
		want mediaInfo
	}{
		{"TvShows/The Expanse/Season 3/the.expanse.s03e09.hdr.1080p.WEBRip.HDR.DDP5.1.H265-d3g.mkv",
			mediaInfo{Kind: "tv", Title: "The Expanse", Season: 3, Episode: 9}},
		{"TvShows/Star Trek - Strange New Worlds/Season 3/Star.Trek.Strange.New.Worlds.S03E09.Terrarium.1080p.WEBRip.10bit.EAC3.5.1.x265-iVy.mkv",
			mediaInfo{Kind: "tv", Title: "Star Trek - Strange New Worlds", Season: 3, Episode: 9, EpTitle: "Terrarium"}},
		{"TvShows/Paw Patrol/Season 7/Paw.Patrol.S07E01-E02.Mighty.Pups.Charged.Up.AAC.2.0.1080p.WEBRip.x265-PoF.mkv",
			mediaInfo{Kind: "tv", Title: "Paw Patrol", Season: 7, Episode: 1, Episode2: 2, EpTitle: "Mighty Pups Charged Up"}},
		{"TvShows/King Of The Hill/Season 5/King Of The Hill.5x11.Hank And The Great Glass Elevator.avi",
			mediaInfo{Kind: "tv", Title: "King Of The Hill", Season: 5, Episode: 11, EpTitle: "Hank And The Great Glass Elevator"}},
		{"TvShows/The Burbs/Season 1/The.Burbs.S01E08.2026.1080p.MAX.WEB-DL.DDP5.1.H.264.mkv",
			mediaInfo{Kind: "tv", Title: "The Burbs", Season: 1, Episode: 8}},
		{"TvShows/Regular Show/Season 7/Regular.Show.S07E24.California.King.1080p.WEBRip.8bit.EAC3.2.0.x265-iVy.mkv",
			mediaInfo{Kind: "tv", Title: "Regular Show", Season: 7, Episode: 24, EpTitle: "California King"}},
		{"Movies/The Empire Strikes Back (1980)/The.Empire.Strikes.Back.1980.1080p.BluRay.DDP.5.1.mkv",
			mediaInfo{Kind: "movie", Title: "The Empire Strikes Back", Year: 1980}},
		{"Movies/3.10.to.Yuma.2007.PROPER.1080p.BluRay.x265.mp4",
			mediaInfo{Kind: "movie", Title: "3 10 to Yuma", Year: 2007}},
	}
	for _, c := range cases {
		if got := parsePath(c.in); got != c.want {
			t.Errorf("%s\n got %+v\nwant %+v", c.in, got, c.want)
		}
	}
}

const sampleSRT = "\ufeff1\r\n00:00:01,000 --> 00:00:02,500\r\n<i>Hello there.</i>\r\n\r\n" +
	"2\n00:00:02,600 --> 00:00:04,000\n{\\an8}General Kenobi!\\NYou are a bold one.\n\n" +
	"3\n00:00:04,000 --> 00:00:05,000\n{\\an8}General Kenobi!\\NYou are a bold one.\n\n" +
	"4\n00:00:06,000 --> 00:00:07,000\nm 0 0 l 100 0 100 100 0 100\n\n" +
	"5\n00:00:08,000 --> 00:00:09,000\n♪ ♪\n\n" +
	"6\n00:00:10,000 --> 00:00:12,000\nI don't like sand.\nIt's coarse and rough.\n"

func TestParseSRT(t *testing.T) {
	cs := parseSRT(sampleSRT)
	want := []string{"Hello there.", "General Kenobi! You are a bold one.", "I don't like sand. It's coarse and rough."}
	if len(cs) != len(want) {
		t.Fatalf("got %d cues: %+v", len(cs), cs)
	}
	for i, w := range want {
		if cs[i].Text != w {
			t.Errorf("cue %d = %q, want %q", i, cs[i].Text, w)
		}
	}
	if cs[1].End != 5000 { // repeat merged into one
		t.Errorf("merged cue end = %d", cs[1].End)
	}
}

func TestSearch(t *testing.T) {
	db, err := openDB(filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatal(err)
	}
	put := func(p, srt string) {
		if _, err := db.put(ingestItem{Path: p, Status: "indexed", SRT: srt}); err != nil {
			t.Fatal(err)
		}
	}
	put("TvShows/Star Wars Clips/Season 1/Clips.S01E03.Sand.mkv", sampleSRT)
	put("Movies/Other Film (1999)/Other.Film.1999.mkv", "1\n00:01:00,000 --> 00:01:02,000\nThe sand is everywhere.\n")
	db.put(ingestItem{Path: "TvShows/Peppa Pig/Season 1/Peppa.Pig.S01E01.mkv", Status: "none"})

	r, err := db.search("coarse rough", filter{}, 10)
	if err != nil || len(r.Hits) != 1 || r.Hits[0].Episode != 3 || r.Hits[0].Start != 10000 {
		t.Fatalf("coarse rough: %+v %v", r, err)
	}
	// split across two subtitles
	r, _ = db.search("you are a bold one hello", filter{}, 10)
	if r.Mode != "all" || len(r.Hits) != 1 {
		t.Fatalf("across lines: %+v", r)
	}
	// stemming: "likes" finds "like"; last word is a prefix while typing
	if r, _ = db.search("likes san", filter{}, 10); len(r.Hits) != 1 {
		t.Fatalf("stem/prefix: %+v", r)
	}
	// filters
	if r, _ = db.search("sand", filter{}, 10); len(r.Hits) != 2 {
		t.Fatalf("sand unfiltered: %+v", r)
	}
	if r, _ = db.search("sand", filter{Kind: "movie"}, 10); len(r.Hits) != 1 || r.Hits[0].Title != "Other Film" {
		t.Fatalf("sand movies: %+v", r)
	}
	if r, _ = db.search("sand", filter{Title: "Star Wars Clips"}, 10); len(r.Hits) != 1 || r.Hits[0].Kind != "tv" {
		t.Fatalf("sand show: %+v", r)
	}
	// exact phrase, and fallback to "some of the words"
	if r, _ = db.search(`"sand is everywhere"`, filter{}, 10); len(r.Hits) != 1 {
		t.Fatalf("phrase: %+v", r)
	}
	if r, _ = db.search("sand zebra", filter{}, 10); r.Mode != "any" || len(r.Hits) != 2 {
		t.Fatalf("fallback: %+v", r)
	}
	// odd input never errors
	for _, q := range []string{`"`, `AND OR NOT`, `*`, `sand"`, `NEAR(`, `'`} {
		if _, err := db.search(q, filter{}, 10); err != nil {
			t.Errorf("%q: %v", q, err)
		}
	}
	// regex: pieces of a quote with anything between, case-insensitive
	if r, err = db.search(`general .+ bold`, filter{Regex: true}, 10); err != nil || len(r.Hits) != 1 || r.Mode != "regex" {
		t.Fatalf("regex: %+v %v", r, err)
	}
	if r, _ = db.search(`colou?r|SAND\b`, filter{Regex: true}, 10); len(r.Hits) != 2 {
		t.Fatalf("regex without required words: %+v", r)
	}
	if _, err = db.search(`(unclosed`, filter{Regex: true}, 10); err == nil {
		t.Fatalf("bad regex should error")
	}
	// re-ingest replaces, titles and stats count
	put("Movies/Other Film (1999)/Other.Film.1999.mkv", "1\n00:00:01,000 --> 00:00:02,000\nNothing here.\n")
	if r, _ = db.search("everywhere", filter{}, 10); len(r.Hits) != 0 {
		t.Fatalf("old lines still searchable: %+v", r)
	}
	st, _ := db.stats()
	if st.Files != 3 || st.Indexed != 2 || st.None != 1 {
		t.Fatalf("stats %+v", st)
	}
	ts, _ := db.titles()
	if len(ts) != 3 {
		t.Fatalf("titles %+v", ts)
	}
	ctx, _ := db.context(1, 10000, 2, 2)
	if len(ctx) != 3 || !ctx[2].Hit {
		t.Fatalf("context %+v", ctx)
	}
}

func TestRequiredWords(t *testing.T) {
	cases := map[string]string{
		`just a little .+ uneven`: "just a little uneven",
		`Just a little \+ uneven`: "just a little uneven",
		`colou?r`:                  "",
		`(cat|dog) food`:           "food",
		`^hello world$`:            "hello world",
		`hel+o there`:              "there",
	}
	for in, want := range cases {
		if got := strings.Join(requiredWords(in), " "); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}
