package main

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"regexp"
	"regexp/syntax"
	"strings"
	"sync"
	"time"
	"unicode"

	"modernc.org/sqlite"
)

// regexp(pattern, text) for SQL, with compiled patterns cached.
var reCache sync.Map

func init() {
	sqlite.MustRegisterDeterministicScalarFunction("regexp", 2, func(ctx *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		pat, _ := args[0].(string)
		text, _ := args[1].(string)
		re, ok := reCache.Load(pat)
		if !ok {
			c, err := regexp.Compile(pat)
			if err != nil {
				return nil, err
			}
			re, _ = reCache.LoadOrStore(pat, c)
		}
		return re.(*regexp.Regexp).MatchString(text), nil
	})
}

const schema = `
CREATE TABLE IF NOT EXISTS media (
  id INTEGER PRIMARY KEY,
  path TEXT UNIQUE NOT NULL,
  size INTEGER NOT NULL DEFAULT 0,
  mtime INTEGER NOT NULL DEFAULT 0,
  kind TEXT NOT NULL,
  title TEXT NOT NULL,
  year INTEGER NOT NULL DEFAULT 0,
  season INTEGER NOT NULL DEFAULT 0,
  episode INTEGER NOT NULL DEFAULT 0,
  episode2 INTEGER NOT NULL DEFAULT 0,
  ep_title TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,          -- indexed | none | image | error
  source TEXT NOT NULL DEFAULT '', -- which subtitle track (or note)
  lines INTEGER NOT NULL DEFAULT 0,
  updated INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS media_title ON media(kind, title);
CREATE TABLE IF NOT EXISTS cues (
  media_id INTEGER NOT NULL,
  idx INTEGER NOT NULL,
  start_ms INTEGER NOT NULL,
  end_ms INTEGER NOT NULL,
  text TEXT NOT NULL,
  PRIMARY KEY (media_id, idx)
) WITHOUT ROWID;
-- one row per pair of neighbouring lines, so a quote split across two subtitles still matches
CREATE VIRTUAL TABLE IF NOT EXISTS win USING fts5(
  text, media_id UNINDEXED, idx UNINDEXED,
  tokenize = 'porter unicode61 remove_diacritics 2'
);
`

type store struct{ db *sql.DB }

func openDB(path string) (*store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	return &store{db: db}, nil
}

// ingestItem is what the extractor sends for one video file.
type ingestItem struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Mtime  int64  `json:"mtime"`
	Status string `json:"status"` // indexed | none | image | error
	Source string `json:"source"`
	SRT    string `json:"srt"`
}

// put replaces everything stored for one file.
func (s *store) put(it ingestItem) (int, error) {
	info := parsePath(it.Path)
	var cues []cue
	if it.Status == "indexed" {
		cues = parseSRT(it.SRT)
		if len(cues) == 0 {
			it.Status, it.Source = "none", strings.TrimSpace(it.Source+" (empty)")
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var id int64
	err = tx.QueryRow(`SELECT id FROM media WHERE path = ?`, it.Path).Scan(&id)
	switch {
	case err == sql.ErrNoRows:
		res, err := tx.Exec(`INSERT INTO media (path, kind, title, status, updated) VALUES (?, '', '', '', 0)`, it.Path)
		if err != nil {
			return 0, err
		}
		id, _ = res.LastInsertId()
	case err != nil:
		return 0, err
	default:
		if _, err := tx.Exec(`DELETE FROM cues WHERE media_id = ?`, id); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`DELETE FROM win WHERE media_id = ?`, id); err != nil {
			return 0, err
		}
	}
	_, err = tx.Exec(`UPDATE media SET size=?, mtime=?, kind=?, title=?, year=?, season=?, episode=?, episode2=?, ep_title=?, status=?, source=?, lines=?, updated=? WHERE id=?`,
		it.Size, it.Mtime, info.Kind, info.Title, info.Year, info.Season, info.Episode, info.Episode2, info.EpTitle, it.Status, it.Source, len(cues), time.Now().Unix(), id)
	if err != nil {
		return 0, err
	}
	cueStmt, err := tx.Prepare(`INSERT INTO cues (media_id, idx, start_ms, end_ms, text) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, err
	}
	winStmt, err := tx.Prepare(`INSERT INTO win (text, media_id, idx) VALUES (?, ?, ?)`)
	if err != nil {
		return 0, err
	}
	for i, c := range cues {
		if _, err := cueStmt.Exec(id, i, c.Start, c.End, c.Text); err != nil {
			return 0, err
		}
		text := c.Text
		if i+1 < len(cues) && cues[i+1].Start-c.End < 4000 {
			text += " " + cues[i+1].Text
		}
		if _, err := winStmt.Exec(text, id, i); err != nil {
			return 0, err
		}
	}
	return len(cues), tx.Commit()
}

type knownFile struct {
	Size   int64  `json:"size"`
	Mtime  int64  `json:"mtime"`
	Status string `json:"status"`
	Source string `json:"source"`
}

func (s *store) known() (map[string]knownFile, error) {
	rows, err := s.db.Query(`SELECT path, size, mtime, status, source FROM media`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]knownFile{}
	for rows.Next() {
		var p string
		var k knownFile
		if err := rows.Scan(&p, &k.Size, &k.Mtime, &k.Status, &k.Source); err != nil {
			return nil, err
		}
		out[p] = k
	}
	return out, rows.Err()
}

// forget removes files that are no longer in the library.
func (s *store) forget(paths []string) (int, error) {
	n := 0
	for _, p := range paths {
		var id int64
		if err := s.db.QueryRow(`SELECT id FROM media WHERE path = ?`, p).Scan(&id); err != nil {
			continue
		}
		s.db.Exec(`DELETE FROM win WHERE media_id = ?`, id)
		s.db.Exec(`DELETE FROM cues WHERE media_id = ?`, id)
		s.db.Exec(`DELETE FROM media WHERE id = ?`, id)
		n++
	}
	return n, nil
}

// ---- searching ----

type line struct {
	Start int    `json:"start"`
	Text  string `json:"text"`
	Hit   bool   `json:"hit,omitempty"`
}

type hit struct {
	MediaID  int64   `json:"mediaId"`
	Kind     string  `json:"kind"`
	Title    string  `json:"title"`
	Year     int     `json:"year,omitempty"`
	Season   int     `json:"season,omitempty"`
	Episode  int     `json:"episode,omitempty"`
	Episode2 int     `json:"episode2,omitempty"`
	EpTitle  string  `json:"epTitle,omitempty"`
	Start    int     `json:"start"`
	Lines    []line  `json:"lines"`
	Score    float64 `json:"score"`
}

type searchResult struct {
	Query   string `json:"query"`
	Mode    string `json:"mode"` // all | any (fell back to "some of the words")
	Hits    []hit  `json:"hits"`
	More    bool   `json:"more"`
	TookMS  int64  `json:"tookMs"`
	Matched int    `json:"matched"`
}

var wordRe = regexp.MustCompile(`[\p{L}\p{N}']+`)

// ftsQuery turns what someone typed into an FTS5 query. "Quoted parts" must appear as a
// phrase; other words must all appear (any=false) or any may appear (any=true).
func ftsQuery(q string, any bool) string {
	var parts []string
	rest := q
	for {
		i := strings.IndexByte(rest, '"')
		if i < 0 {
			break
		}
		j := strings.IndexByte(rest[i+1:], '"')
		if j < 0 {
			break
		}
		if words := wordRe.FindAllString(rest[i+1:i+1+j], -1); len(words) > 0 {
			parts = append(parts, `"`+strings.Join(words, " ")+`"`)
		}
		rest = rest[:i] + " " + rest[i+1+j+1:]
	}
	words := wordRe.FindAllString(rest, -1)
	for i, w := range words {
		w = strings.Trim(w, "'")
		if w == "" {
			continue
		}
		t := `"` + w + `"`
		if i == len(words)-1 && len([]rune(w)) >= 3 && !strings.HasSuffix(q, " ") {
			t += "*" // still typing the last word
		}
		parts = append(parts, t)
	}
	if any {
		return strings.Join(parts, " OR ")
	}
	return strings.Join(parts, " AND ")
}

type filter struct {
	Kind  string // tv | movie | ""
	Title string // exact show/movie title, or ""
	Regex bool   // q is a regular expression (case-insensitive)
}

type badQuery struct{ msg string }

func (e badQuery) Error() string { return e.msg }

// requiredWords finds whole words a regex can't match without, so the full-text index can
// narrow the search before the regex runs. Only words fully inside a run of literal text
// count: "colou?r" gives nothing, "just a little .+ uneven" gives just, a, little, uneven.
func requiredWords(pattern string) []string {
	re, err := syntax.Parse(pattern, syntax.Perl|syntax.FoldCase)
	if err != nil {
		return nil
	}
	re = re.Simplify()
	var subs []*syntax.Regexp
	switch re.Op {
	case syntax.OpConcat:
		subs = re.Sub
	case syntax.OpLiteral:
		subs = []*syntax.Regexp{re}
	default:
		return nil
	}
	var words []string
	for i, sub := range subs {
		if sub.Op != syntax.OpLiteral {
			continue
		}
		lit := []rune(string(sub.Rune))
		isWord := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '\'' }
		start := 0
		for j := 0; j <= len(lit); j++ {
			if j < len(lit) && isWord(lit[j]) {
				continue
			}
			if j > start {
				// a word at the edge of this literal could continue into the next piece
				leftOK := start > 0 || i == 0 || isBoundary(subs[i-1])
				rightOK := j < len(lit) || i == len(subs)-1 || isBoundary(subs[i+1])
				if leftOK && rightOK {
					words = append(words, strings.ToLower(string(lit[start:j])))
				}
			}
			start = j + 1
		}
	}
	return words
}

func isBoundary(r *syntax.Regexp) bool {
	switch r.Op {
	case syntax.OpBeginLine, syntax.OpEndLine, syntax.OpBeginText, syntax.OpEndText, syntax.OpWordBoundary:
		return true
	}
	return false
}

func (s *store) search(q string, f filter, limit int) (searchResult, error) {
	t0 := time.Now()
	res := searchResult{Query: q, Mode: "all", Hits: []hit{}}
	if strings.TrimSpace(q) == "" {
		return res, nil
	}
	if f.Regex {
		res.Mode = "regex"
		hits, err := s.matchRegex(q, f, limit)
		if err != nil {
			return res, err
		}
		res.Matched = len(hits)
		if len(hits) > limit {
			hits, res.More = hits[:limit], true
		}
		for i := range hits {
			if err := s.fill(&hits[i]); err != nil {
				return res, err
			}
		}
		res.Hits = hits
		res.TookMS = time.Since(t0).Milliseconds()
		return res, nil
	}
	hits, err := s.match(ftsQuery(q, false), f, limit)
	if err != nil {
		return res, err
	}
	if len(hits) == 0 && len(wordRe.FindAllString(q, -1)) > 1 {
		res.Mode = "any"
		if hits, err = s.match(ftsQuery(q, true), f, limit); err != nil {
			return res, err
		}
	}
	res.Matched = len(hits)
	if len(hits) > limit {
		hits, res.More = hits[:limit], true
	}
	for i := range hits {
		if err := s.fill(&hits[i]); err != nil {
			return res, err
		}
	}
	res.Hits = hits
	res.TookMS = time.Since(t0).Milliseconds()
	return res, nil
}

// match runs the FTS query and keeps the best window per spot (neighbouring windows
// overlap, so one quote usually matches two of them).
func (s *store) match(fq string, f filter, limit int) ([]hit, error) {
	if fq == "" {
		return nil, nil
	}
	sqlq := `SELECT win.media_id, win.idx, bm25(win) AS r, m.kind, m.title, m.year, m.season, m.episode, m.episode2, m.ep_title
	         FROM win JOIN media m ON m.id = win.media_id
	         WHERE win MATCH ?`
	args := []any{fq}
	if f.Kind != "" {
		sqlq += ` AND m.kind = ?`
		args = append(args, f.Kind)
	}
	if f.Title != "" {
		sqlq += ` AND m.title = ?`
		args = append(args, f.Title)
	}
	sqlq += ` ORDER BY r LIMIT ?`
	args = append(args, limit*4+20)
	rows, err := s.db.Query(sqlq, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type spot struct {
		id  int64
		idx int
	}
	seen := map[spot]bool{}
	var out []hit
	for rows.Next() {
		var h hit
		var idx int
		if err := rows.Scan(&h.MediaID, &idx, &h.Score, &h.Kind, &h.Title, &h.Year, &h.Season, &h.Episode, &h.Episode2, &h.EpTitle); err != nil {
			return nil, err
		}
		if seen[spot{h.MediaID, idx - 1}] || seen[spot{h.MediaID, idx}] || seen[spot{h.MediaID, idx + 1}] {
			continue
		}
		seen[spot{h.MediaID, idx}] = true
		h.Start = idx // replaced with the time in fill
		out = append(out, h)
	}
	return out, rows.Err()
}

// matchRegex runs a case-insensitive regular expression over pairs of neighbouring lines,
// using the full-text index first when the pattern contains whole words it needs.
func (s *store) matchRegex(pattern string, f filter, limit int) ([]hit, error) {
	if _, err := regexp.Compile(pattern); err != nil {
		return nil, badQuery{"That regular expression doesn't work: " + strings.TrimPrefix(err.Error(), "error parsing regexp: ")}
	}
	pat := "(?i)" + pattern
	sqlq := `SELECT win.media_id, win.idx, m.kind, m.title, m.year, m.season, m.episode, m.episode2, m.ep_title
	         FROM win JOIN media m ON m.id = win.media_id WHERE regexp(?, win.text)`
	args := []any{pat}
	if words := requiredWords(pattern); len(words) > 0 {
		var q []string
		for _, w := range words {
			q = append(q, `"`+strings.Trim(w, "'")+`"`)
		}
		sqlq += ` AND win MATCH ?`
		args = append(args, strings.Join(q, " AND "))
	}
	if f.Kind != "" {
		sqlq += ` AND m.kind = ?`
		args = append(args, f.Kind)
	}
	if f.Title != "" {
		sqlq += ` AND m.title = ?`
		args = append(args, f.Title)
	}
	sqlq += ` ORDER BY m.kind DESC, m.title, m.year, m.season, m.episode, win.idx LIMIT ?`
	args = append(args, limit*4+20)
	rows, err := s.db.Query(sqlq, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []hit
	last := map[int64]int{}
	for rows.Next() {
		var h hit
		var idx int
		if err := rows.Scan(&h.MediaID, &idx, &h.Kind, &h.Title, &h.Year, &h.Season, &h.Episode, &h.Episode2, &h.EpTitle); err != nil {
			return nil, err
		}
		if p, ok := last[h.MediaID]; ok && idx-p <= 1 { // same spot via the overlapping window
			continue
		}
		last[h.MediaID] = idx
		h.Start = idx
		out = append(out, h)
	}
	return out, rows.Err()
}

// fill adds the matched lines plus two lines either side for context.
func (s *store) fill(h *hit) error {
	idx := h.Start
	rows, err := s.db.Query(`SELECT idx, start_ms, text FROM cues WHERE media_id = ? AND idx BETWEEN ? AND ? ORDER BY idx`, h.MediaID, idx-2, idx+3)
	if err != nil {
		return err
	}
	defer rows.Close()
	h.Lines = []line{}
	for rows.Next() {
		var i int
		var l line
		if err := rows.Scan(&i, &l.Start, &l.Text); err != nil {
			return err
		}
		l.Hit = i == idx || i == idx+1
		if i == idx {
			h.Start = l.Start
		}
		h.Lines = append(h.Lines, l)
	}
	return rows.Err()
}

// context returns a longer stretch of dialogue around a moment.
func (s *store) context(mediaID int64, startMS, before, after int) ([]line, error) {
	var idx int
	err := s.db.QueryRow(`SELECT idx FROM cues WHERE media_id = ? AND start_ms <= ? ORDER BY idx DESC LIMIT 1`, mediaID, startMS).Scan(&idx)
	if err == sql.ErrNoRows {
		idx = 0
	} else if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT idx, start_ms, text FROM cues WHERE media_id = ? AND idx BETWEEN ? AND ? ORDER BY idx`, mediaID, idx-before, idx+after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []line{}
	for rows.Next() {
		var i int
		var l line
		if err := rows.Scan(&i, &l.Start, &l.Text); err != nil {
			return nil, err
		}
		l.Hit = i == idx
		out = append(out, l)
	}
	return out, rows.Err()
}

type titleRow struct {
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	Year     int    `json:"year,omitempty"`
	Files    int    `json:"files"`
	Indexed  int    `json:"indexed"`
	Image    int    `json:"image"`
	Missing  int    `json:"missing"`
	Episodes int    `json:"episodes"`
}

func (s *store) titles() ([]titleRow, error) {
	rows, err := s.db.Query(`SELECT kind, title, MAX(year), COUNT(*),
	  SUM(status = 'indexed'), SUM(status = 'image'), SUM(status IN ('none', 'error')), COUNT(DISTINCT season * 1000 + episode)
	  FROM media GROUP BY kind, title ORDER BY kind DESC, title COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []titleRow{}
	for rows.Next() {
		var t titleRow
		if err := rows.Scan(&t.Kind, &t.Title, &t.Year, &t.Files, &t.Indexed, &t.Image, &t.Missing, &t.Episodes); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

type stats struct {
	Files   int   `json:"files"`
	Indexed int   `json:"indexed"`
	Image   int   `json:"image"`
	None    int   `json:"none"`
	Errors  int   `json:"errors"`
	Lines   int   `json:"lines"`
	Last    int64 `json:"lastUpdate"`
}

func (s *store) stats() (stats, error) {
	var st stats
	err := s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(status='indexed'),0), COALESCE(SUM(status='image'),0),
	  COALESCE(SUM(status='none'),0), COALESCE(SUM(status='error'),0), COALESCE(SUM(lines),0), COALESCE(MAX(updated),0) FROM media`).
		Scan(&st.Files, &st.Indexed, &st.Image, &st.None, &st.Errors, &st.Lines, &st.Last)
	return st, err
}
