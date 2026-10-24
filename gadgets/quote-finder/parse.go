package main

import (
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// mediaInfo is what we can tell about a video from where it sits in the library.
type mediaInfo struct {
	Kind     string // "tv" or "movie"
	Title    string // show or movie title
	Year     int
	Season   int
	Episode  int
	Episode2 int    // second episode in double-episode files (S07E01-E02)
	EpTitle  string // episode title from the file name, when there is one
}

var (
	seRe      = regexp.MustCompile(`(?i)\bS(\d{1,2})[ ._-]?E(\d{1,3})(?:[ ._-]?-?[ ._-]?E?(\d{1,3}))?\b`)
	xRe       = regexp.MustCompile(`(?i)\b(\d{1,2})x(\d{1,3})\b`)
	seasonRe  = regexp.MustCompile(`(?i)^season[ ._-]*(\d+)$`)
	yearRe    = regexp.MustCompile(`[(\[. _-]((?:19|20)\d\d)[)\]. _-]`)
	junkStart = regexp.MustCompile(`(?i)[ ._-](\d{3,4}p|2160p|4k|uhd|web[ ._-]?(rip|dl)?|webrip|bluray|blu[ ._-]?ray|brrip|bdrip|hdtv|dvdrip|x26[45]|h[ ._]?26[45]|hevc|aac\d?|ac3|eac3|ddp?\d|dts|10bit|8bit|hdr|repack|proper|amzn|nf|max|dsnp|atvp|internal|multi|dual)([ ._-]|$)`)
	spaceRe   = regexp.MustCompile(`\s+`)
	leadYear  = regexp.MustCompile(`^(19|20)\d\d( |$)`)
)

func clean(s string) string {
	s = strings.NewReplacer(".", " ", "_", " ").Replace(s)
	return strings.TrimSpace(strings.Trim(spaceRe.ReplaceAllString(s, " "), " -"))
}

// beforeJunk cuts a release name at the first quality/codec tag.
func beforeJunk(s string) string {
	if loc := junkStart.FindStringIndex(" " + s + " "); loc != nil {
		if i := loc[0] - 1; i >= 0 && i <= len(s) {
			s = s[:i]
		}
	}
	return s
}

// parsePath reads TvShows/<Show>/Season N/<file> and Movies/<Title (Year)>/<file> layouts.
func parsePath(p string) mediaInfo {
	p = strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(p, "\\", "/")), "/")
	parts := strings.Split(p, "/")
	file := strings.TrimSuffix(parts[len(parts)-1], path.Ext(parts[len(parts)-1]))
	top := strings.ToLower(parts[0])
	var m mediaInfo

	if strings.Contains(top, "tv") || strings.Contains(top, "show") || seRe.MatchString(file) {
		m.Kind = "tv"
		if len(parts) >= 3 && (strings.Contains(top, "tv") || strings.Contains(top, "show")) {
			m.Title = parts[1]
		}
		if sm := seRe.FindStringSubmatchIndex(file); sm != nil {
			m.Season, _ = strconv.Atoi(file[sm[2]:sm[3]])
			m.Episode, _ = strconv.Atoi(file[sm[4]:sm[5]])
			if sm[6] >= 0 {
				if e2, _ := strconv.Atoi(file[sm[6]:sm[7]]); e2 > m.Episode && e2-m.Episode < 5 {
					m.Episode2 = e2
				}
			}
			if m.Title == "" {
				m.Title = clean(yearRe.ReplaceAllString(file[:sm[0]]+" ", " "))
			}
			m.EpTitle = clean(beforeJunk(file[sm[1]:]))
		} else if xm := xRe.FindStringSubmatchIndex(file); xm != nil {
			m.Season, _ = strconv.Atoi(file[xm[2]:xm[3]])
			m.Episode, _ = strconv.Atoi(file[xm[4]:xm[5]])
			m.EpTitle = clean(beforeJunk(file[xm[1]:]))
		}
		if m.Season == 0 && len(parts) >= 3 {
			if sm := seasonRe.FindStringSubmatch(parts[len(parts)-2]); sm != nil {
				m.Season, _ = strconv.Atoi(sm[1])
			}
		}
		// a leading year in the episode title is part of the release name ("S01E08 2026 1080p")
		if leadYear.MatchString(m.EpTitle) {
			m.EpTitle = strings.TrimSpace(m.EpTitle[4:])
		}
		if m.Title == "" {
			m.Title = clean(file)
		}
		return m
	}

	m.Kind = "movie"
	name := file
	if len(parts) >= 3 { // Movies/<Folder>/<file>: folder names are usually tidier
		name = parts[len(parts)-2]
	}
	if y := yearRe.FindStringSubmatchIndex(" " + name + " "); y != nil {
		m.Year, _ = strconv.Atoi((" " + name + " ")[y[2]:y[3]])
		name = name[:maxInt(0, y[0]-1)]
	} else {
		name = beforeJunk(name)
	}
	m.Title = strings.TrimRightFunc(clean(name), func(r rune) bool { return r == '(' || r == '[' || unicode.IsSpace(r) })
	if m.Title == "" {
		m.Title = clean(file)
	}
	return m
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ---- subtitles ----

type cue struct {
	Start, End int // milliseconds
	Text       string
}

var (
	timeRe    = regexp.MustCompile(`(\d+):(\d\d):(\d\d)[,.](\d{1,3})\s*-->\s*(\d+):(\d\d):(\d\d)[,.](\d{1,3})`)
	tagRe     = regexp.MustCompile(`<[^>]*>|\{[^}]*\}`)
	drawingRe = regexp.MustCompile(`^m(\s+-?\d+(\.\d+)?)+(\s+[lbm](\s+-?\d+(\.\d+)?)+)*$`)
)

func ms(h, m, s, f string) int {
	hh, _ := strconv.Atoi(h)
	mm, _ := strconv.Atoi(m)
	ss, _ := strconv.Atoi(s)
	for len(f) < 3 {
		f += "0"
	}
	ff, _ := strconv.Atoi(f)
	return ((hh*60+mm)*60+ss)*1000 + ff
}

// cleanCue strips formatting tags and ASS leftovers from one subtitle's text.
func cleanCue(s string) string {
	s = strings.NewReplacer(`\N`, " ", `\n`, " ", `\h`, " ").Replace(s)
	s = tagRe.ReplaceAllString(s, "")
	s = strings.Join(strings.Fields(s), " ")
	if drawingRe.MatchString(s) { // vector drawing commands from styled ASS subtitles
		return ""
	}
	if strings.Trim(s, "♪♫#*-–— .") == "" {
		return ""
	}
	return s
}

// parseSRT reads SubRip text into cues, dropping empty ones and merging exact repeats
// (styled subtitles often show one line several times at once).
func parseSRT(srt string) []cue {
	srt = strings.ReplaceAll(strings.TrimPrefix(srt, "\ufeff"), "\r\n", "\n")
	var out []cue
	for _, block := range strings.Split(srt, "\n\n") {
		lines := strings.Split(strings.TrimSpace(block), "\n")
		for i, l := range lines {
			tm := timeRe.FindStringSubmatch(l)
			if tm == nil {
				continue
			}
			text := cleanCue(strings.Join(lines[i+1:], " "))
			if text == "" {
				break
			}
			c := cue{Start: ms(tm[1], tm[2], tm[3], tm[4]), End: ms(tm[5], tm[6], tm[7], tm[8]), Text: text}
			if n := len(out); n > 0 && out[n-1].Text == c.Text && c.Start-out[n-1].End < 1500 {
				if c.End > out[n-1].End {
					out[n-1].End = c.End
				}
				break
			}
			out = append(out, c)
			break
		}
	}
	// styled subtitles can arrive out of order
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Start < out[j-1].Start; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
