package media

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
)

// JoinOptions controls how clips are joined.
type JoinOptions struct {
	Transition string  `json:"transition"` // none | fade (dip to black) | crossfade
	Seconds    float64 `json:"seconds"`    // transition length
	Mode       string  `json:"mode"`       // auto | copy | reencode
	FadeEdges  bool    `json:"fade_edges"` // fade in at the very start and out at the very end too
	MaxHeight  int     `json:"-"`
}

// Target is the common format every clip is converted to when re-encoding.
type Target struct {
	W, H int
	FPS  string  // ffmpeg rate expression, e.g. "30000/1001"
	FPSf float64 // same, as a number
}

// Plan is what Join will do, decided before any work starts.
type Plan struct {
	Copy     bool     // stream copy, no re-encoding
	Why      []string // why stream copy isn't possible (empty when Copy)
	Target   Target
	Seconds  float64 // transition length after clamping
	Ext      string  // output extension
	Duration float64 // output duration
	EstBytes int64   // rough peak disk use (intermediates + output)
}

var standardRates = []struct {
	expr string
	v    float64
}{{"24000/1001", 23.976}, {"24", 24}, {"25", 25}, {"30000/1001", 29.97}, {"30", 30},
	{"50", 50}, {"60000/1001", 59.94}, {"60", 60}}

// snapFPS maps a measured (often variable) frame rate onto the nearest standard rate, max 60.
func snapFPS(f float64) (string, float64) {
	if f <= 0 || math.IsNaN(f) {
		return "30", 30
	}
	if f > 60 {
		return "60", 60
	}
	best := standardRates[0]
	for _, r := range standardRates {
		if math.Abs(r.v-f) < math.Abs(best.v-f) {
			best = r
		}
	}
	return best.expr, best.v
}

// CopyBlockers lists reasons the clips can't be joined by stream copy.
func CopyBlockers(in []Info) []string {
	var why []string
	if len(in) < 2 {
		return nil
	}
	a := in[0]
	for i, b := range in[1:] {
		n := fmt.Sprintf("clip %d", i+2)
		switch {
		case b.Video.CodecName != a.Video.CodecName:
			why = append(why, fmt.Sprintf("%s video is %s, clip 1 is %s", n, b.Video.CodecName, a.Video.CodecName))
		case b.Video.Profile != a.Video.Profile:
			why = append(why, fmt.Sprintf("%s uses %s profile %q, clip 1 %q", n, b.Video.CodecName, b.Video.Profile, a.Video.Profile))
		}
		if b.Video.Width != a.Video.Width || b.Video.Height != a.Video.Height {
			why = append(why, fmt.Sprintf("%s is %dx%d, clip 1 is %dx%d", n, b.Video.Width, b.Video.Height, a.Video.Width, a.Video.Height))
		}
		if b.Video.PixFmt != a.Video.PixFmt {
			why = append(why, fmt.Sprintf("%s pixel format %s ≠ %s", n, b.Video.PixFmt, a.Video.PixFmt))
		}
		if b.Rotation != a.Rotation {
			why = append(why, fmt.Sprintf("%s is rotated %d°, clip 1 %d°", n, b.Rotation, a.Rotation))
		}
		if (a.Audio == nil) != (b.Audio == nil) {
			why = append(why, fmt.Sprintf("only some clips have sound (clip 1: %v, %s: %v)", a.Audio != nil, n, b.Audio != nil))
		} else if a.Audio != nil && (a.Audio.CodecName != b.Audio.CodecName || a.Audio.SampleRate != b.Audio.SampleRate ||
			a.Audio.Channels != b.Audio.Channels) {
			why = append(why, fmt.Sprintf("%s audio is %s %s Hz %dch, clip 1 %s %s Hz %dch", n,
				b.Audio.CodecName, b.Audio.SampleRate, b.Audio.Channels, a.Audio.CodecName, a.Audio.SampleRate, a.Audio.Channels))
		}
	}
	return why
}

func mp4Friendly(in Info) bool {
	v := map[string]bool{"h264": true, "hevc": true, "av1": true, "mpeg4": true}
	a := map[string]bool{"aac": true, "mp3": true, "alac": true, "ac3": true, "eac3": true}
	return v[in.VCodec] && (in.Audio == nil || a[in.ACodec])
}

// PlanJoin decides between stream copy and re-encode and works out the common format.
func PlanJoin(in []Info, o JoinOptions) (Plan, error) {
	if len(in) < 2 {
		return Plan{}, fmt.Errorf("need at least two clips to join")
	}
	p := Plan{Seconds: o.Seconds}
	for _, c := range in {
		p.Duration += c.Duration
	}
	switch o.Transition {
	case "", "none":
		o.Transition = "none"
	case "fade", "crossfade":
		if p.Seconds <= 0 {
			p.Seconds = 0.5
		}
	default:
		return Plan{}, fmt.Errorf("unknown transition %q (none, fade, crossfade)", o.Transition)
	}
	why := CopyBlockers(in)
	if o.Transition != "none" {
		why = append([]string{"transitions need re-encoding"}, why...)
	}
	switch o.Mode {
	case "copy":
		if len(why) > 0 {
			return Plan{}, fmt.Errorf("can't join without re-encoding: %s", strings.Join(why, "; "))
		}
		p.Copy = true
	case "reencode":
		p.Copy, p.Why = false, append([]string{"re-encode was requested"}, why...)
	case "", "auto":
		p.Copy, p.Why = len(why) == 0, why
	default:
		return Plan{}, fmt.Errorf("unknown mode %q (auto, copy, reencode)", o.Mode)
	}
	if p.Copy {
		p.Ext = ".mkv"
		if mp4Friendly(in[0]) {
			p.Ext = ".mp4"
		}
		for _, c := range in {
			p.EstBytes += c.Size
		}
		return p, nil
	}
	// re-encode target: first clip's displayed size (capped), nearest standard frame rate
	w, h := in[0].DispW, in[0].DispH
	if o.MaxHeight > 0 && h > o.MaxHeight {
		w, h = w*o.MaxHeight/h, o.MaxHeight
	}
	p.Target = Target{W: w &^ 1, H: h &^ 1}
	p.Target.FPS, p.Target.FPSf = snapFPS(in[0].FPS)
	p.Ext = ".mp4"
	if o.Transition != "none" {
		minDur := math.Inf(1)
		for _, c := range in {
			minDur = math.Min(minDur, c.Duration)
		}
		p.Seconds = math.Max(0.1, math.Min(p.Seconds, minDur/3))
		if o.Transition == "crossfade" {
			p.Duration -= float64(len(in)-1) * p.Seconds
		}
	}
	vb := math.Max(5e5, float64(p.Target.W*p.Target.H)*p.Target.FPSf*0.07) // ~CRF 20 veryfast
	segs := p.Duration * (vb + 1.536e6) / 8                                // H.264 + PCM intermediates
	final := p.Duration * (vb + 192e3) / 8
	p.EstBytes = int64(segs + final)
	return p, nil
}

// segment is one piece of the re-encoded timeline: a stretch of one clip, or a crossfade of two.
type segment struct {
	a, b            int     // clip indexes (b = -1 for a plain stretch)
	start, dur      float64 // in clip a
	fadeIn, fadeOut float64
}

func buildSegments(in []Info, o JoinOptions, p Plan) []segment {
	n := len(in)
	var segs []segment
	for i, c := range in {
		switch o.Transition {
		case "crossfade":
			head, tail := 0.0, 0.0
			if i > 0 {
				head = p.Seconds
			}
			if i < n-1 {
				tail = p.Seconds
			}
			s := segment{a: i, b: -1, start: head, dur: c.Duration - head - tail}
			if o.FadeEdges && i == 0 {
				s.fadeIn = p.Seconds
			}
			if o.FadeEdges && i == n-1 {
				s.fadeOut = p.Seconds
			}
			segs = append(segs, s)
			if i < n-1 {
				segs = append(segs, segment{a: i, b: i + 1, start: c.Duration - p.Seconds, dur: p.Seconds})
			}
		case "fade":
			f := math.Min(p.Seconds, c.Duration/3)
			s := segment{a: i, b: -1, dur: c.Duration}
			if i > 0 || o.FadeEdges {
				s.fadeIn = f
			}
			if i < n-1 || o.FadeEdges {
				s.fadeOut = f
			}
			segs = append(segs, s)
		default:
			segs = append(segs, segment{a: i, b: -1, dur: c.Duration})
		}
	}
	return segs
}

func fs(f float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.4f", f), "0"), ".")
}

// segmentArgs builds the ffmpeg command for one segment. Each segment decodes at most two short
// inputs and encodes one output, so memory stays flat no matter how many clips are joined.
func segmentArgs(in []Info, s segment, t Target, threads int, out string) []string {
	th := fmt.Sprint(threads)
	var args []string
	inputs := 0
	addClip := func(i int, start, dur float64) (v, a string) {
		if start > 0.001 {
			args = append(args, "-ss", fs(start))
		}
		args = append(args, "-threads", th, "-t", fs(dur+0.5), "-i", in[i].Path)
		v = fmt.Sprintf("[%d:v:0]", inputs)
		a = fmt.Sprintf("[%d:a:0]", inputs)
		inputs++
		if in[i].Audio == nil { // silent clip: synthesise silence so every segment has sound
			args = append(args, "-f", "lavfi", "-t", fs(dur+0.5), "-i", "anullsrc=r=48000:cl=stereo")
			a = fmt.Sprintf("[%d:a:0]", inputs)
			inputs++
		}
		return v, a
	}
	norm := func(d float64) string {
		return fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2:color=black,"+
			"setsar=1,fps=%s,tpad=stop_mode=clone:stop_duration=1,trim=duration=%s,setpts=PTS-STARTPTS,format=yuv420p",
			t.W, t.H, t.W, t.H, t.FPS, fs(d))
	}
	anorm := func(d float64) string {
		return fmt.Sprintf("aresample=48000,aformat=sample_fmts=s16:sample_rates=48000:channel_layouts=stereo,"+
			"apad=whole_dur=%s,atrim=duration=%s,asetpts=PTS-STARTPTS", fs(d), fs(d))
	}
	var fc string
	if s.b < 0 {
		v, a := addClip(s.a, s.start, s.dur)
		vf, af := norm(s.dur), anorm(s.dur)
		if s.fadeIn > 0 {
			vf += ",fade=t=in:st=0:d=" + fs(s.fadeIn)
			af += ",afade=t=in:st=0:d=" + fs(s.fadeIn)
		}
		if s.fadeOut > 0 {
			st := fs(s.dur - s.fadeOut)
			vf += ",fade=t=out:st=" + st + ":d=" + fs(s.fadeOut)
			af += ",afade=t=out:st=" + st + ":d=" + fs(s.fadeOut)
		}
		fc = fmt.Sprintf("%s%s[v];%s%s[a]", v, vf, a, af)
	} else {
		va, aa := addClip(s.a, s.start, s.dur)
		vb, ab := addClip(s.b, 0, s.dur)
		d := fs(s.dur)
		// ffmpeg 7 reports the frame rate as unknown after trim/setpts, and xfade refuses to run
		// without one. A second fps= (a no-op: frames are already at that rate) restores it.
		xv := norm(s.dur) + ",fps=" + t.FPS
		fc = fmt.Sprintf("%s%s[va];%s%s[vb];[va][vb]xfade=transition=fade:duration=%s:offset=0[v];"+
			"%s%s[xa];%s%s[xb];[xa][xb]acrossfade=d=%s:c1=tri:c2=tri[a]",
			va, xv, vb, xv, d, aa, anorm(s.dur), ab, anorm(s.dur), d)
	}
	args = append(args, "-filter_complex", fc, "-filter_complex_threads", "1", "-map", "[v]", "-map", "[a]",
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "20", "-pix_fmt", "yuv420p", "-threads", th,
		"-c:a", "pcm_s16le", "-t", fs(s.dur), out)
	return args
}

// Join executes a plan. progress gets 0-100 and a short message. Intermediates go in work and are
// deleted as soon as the output is written.
func Join(ctx context.Context, in []Info, o JoinOptions, p Plan, work, out string, ff FF,
	progress func(pct int, msg string)) error {
	list := filepath.Join(work, "list.txt")
	writeList := func(paths []string) error {
		var b strings.Builder
		for _, x := range paths {
			fmt.Fprintf(&b, "file '%s'\n", strings.ReplaceAll(x, "'", `'\''`))
		}
		return os.WriteFile(list, []byte(b.String()), 0o644)
	}
	faststart := func(final int64) []string {
		// +faststart rewrites the whole file once more at the end; skip it if space is tight
		if Free(filepath.Dir(out)) > final*2+256<<20 {
			return []string{"-movflags", "+faststart"}
		}
		return nil
	}
	if p.Copy {
		paths := make([]string, len(in))
		for i, c := range in {
			paths[i] = c.Path
		}
		if err := writeList(paths); err != nil {
			return err
		}
		args := []string{"-f", "concat", "-safe", "0", "-i", list, "-map", "0:v:0"}
		if in[0].Audio != nil {
			args = append(args, "-map", "0:a:0")
		}
		args = append(args, "-c", "copy", "-dn", "-sn", "-avoid_negative_ts", "make_zero")
		if p.Ext == ".mp4" {
			args = append(args, faststart(p.EstBytes)...)
			if in[0].VCodec == "hevc" {
				args = append(args, "-tag:v", "hvc1") // plays on Apple devices
			}
		}
		args = append(args, out)
		progress(1, "joining without re-encoding")
		return ff.Run(ctx, args, func(sec float64) {
			progress(max(1, int(99*sec/p.Duration)), "joining without re-encoding")
		})
	}

	segs := buildSegments(in, o, p)
	var total float64
	for _, s := range segs {
		total += s.dur
	}
	var done float64
	var paths []string
	for i, s := range segs {
		seg := filepath.Join(work, fmt.Sprintf("seg_%03d.mov", i))
		what := fmt.Sprintf("encoding clip %d of %d", s.a+1, len(in))
		if s.b >= 0 {
			what = fmt.Sprintf("crossfading clip %d into %d", s.a+1, s.b+1)
		}
		base := done
		err := ff.Run(ctx, segmentArgs(in, s, p.Target, ff.Threads, seg), func(sec float64) {
			progress(int(95*(base+math.Min(sec, s.dur))/total), what)
		})
		if err != nil {
			return fmt.Errorf("%s: %w", what, err)
		}
		done += s.dur
		paths = append(paths, seg)
	}
	if err := writeList(paths); err != nil {
		return err
	}
	progress(96, "writing the final file")
	args := []string{"-f", "concat", "-safe", "0", "-i", list, "-map", "0:v:0", "-map", "0:a:0",
		"-c:v", "copy", "-c:a", "aac", "-b:a", "192k"}
	args = append(args, faststart(int64(total*(float64(p.Target.W*p.Target.H)*p.Target.FPSf*0.07+192e3)/8))...)
	args = append(args, out)
	err := ff.Run(ctx, args, nil)
	for _, x := range paths {
		_ = os.Remove(x)
	}
	return err
}
