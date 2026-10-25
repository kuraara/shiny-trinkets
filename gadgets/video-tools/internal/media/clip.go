package media

import (
	"fmt"
	"strings"
)

// ClipOptions describe one cut.
type ClipOptions struct {
	Start, End float64 // seconds into the source
	// Mode: "accurate" re-encodes to H.264/AAC MP4 and starts exactly at Start (plays anywhere);
	// "fast" copies the streams (no quality loss, seconds) but can only start on a keyframe.
	Mode      string
	MaxHeight int    // accurate: scale down to this height (0 = keep)
	Subtitles string // accurate: SRT file to burn in (times relative to Start)
	FontsDir  string
}

// IsHDR reports a PQ or HLG source, which looks washed out unless tone-mapped to SDR.
func IsHDR(in Info) bool {
	t := in.Video.ColorTrans
	return t == "smpte2084" || t == "arib-std-b67"
}

// ClipExt is the output extension for a cut.
func ClipExt(in Info, o ClipOptions) string {
	if o.Mode == "fast" && !mp4Friendly(in) {
		return ".mkv"
	}
	return ".mp4"
}

// ClipArgs builds the ffmpeg arguments for a cut.
func ClipArgs(in Info, o ClipOptions, threads int, out string) []string {
	d := o.End - o.Start
	a := []string{"-ss", fs(o.Start), "-i", in.Path, "-t", fs(d), "-map", "0:v:0"}
	if in.Audio != nil {
		a = append(a, "-map", fmt.Sprintf("0:%d", in.Audio.Index))
	}
	a = append(a, "-sn", "-dn", "-map_metadata", "-1", "-map_chapters", "-1")
	if o.Mode == "fast" {
		a = append(a, "-c", "copy", "-avoid_negative_ts", "make_zero")
		if strings.HasSuffix(out, ".mp4") {
			a = append(a, "-movflags", "+faststart")
			if in.VCodec == "hevc" {
				a = append(a, "-tag:v", "hvc1")
			}
		}
		return append(a, out)
	}
	var vf []string
	if IsHDR(in) { // tone-map PQ/HLG to ordinary BT.709 so it doesn't look grey
		vf = append(vf, "zscale=t=linear:npl=100", "format=gbrpf32le", "zscale=p=bt709",
			"tonemap=tonemap=hable:desat=0", "zscale=t=bt709:m=bt709:r=tv")
	}
	if o.MaxHeight > 0 && in.DispH > o.MaxHeight {
		vf = append(vf, fmt.Sprintf("scale=-2:%d", o.MaxHeight))
	} else {
		vf = append(vf, "scale=trunc(iw/2)*2:trunc(ih/2)*2") // H.264 4:2:0 needs even sizes
	}
	vf = append(vf, "setsar=1")
	if o.Subtitles != "" {
		vf = append(vf, fmt.Sprintf("subtitles=filename='%s':fontsdir='%s':force_style='%s'", o.Subtitles, o.FontsDir,
			"FontName=Inter,Bold=1,FontSize=20,PrimaryColour=&H00FFFFFF,OutlineColour=&H00000000,BorderStyle=1,Outline=2,Shadow=0,MarginV=22"))
	}
	vf = append(vf, "format=yuv420p")
	a = append(a, "-vf", strings.Join(vf, ","), "-c:v", "libx264", "-preset", "veryfast", "-crf", "20",
		"-profile:v", "high", "-threads", fmt.Sprint(max(threads, 1)))
	if in.Audio != nil {
		a = append(a, "-c:a", "aac", "-b:a", "160k", "-ac", "2")
	}
	return append(a, "-movflags", "+faststart", out)
}

// SRT writes subtitle cues (seconds relative to the clip start) as an SRT file body.
func SRT(cues []Cue) string {
	var b strings.Builder
	ts := func(f float64) string {
		ms := int(f*1000 + 0.5)
		return fmt.Sprintf("%02d:%02d:%02d,%03d", ms/3600000, ms/60000%60, ms/1000%60, ms%1000)
	}
	n := 0
	for _, c := range cues {
		if c.End <= 0 || strings.TrimSpace(c.Text) == "" {
			continue
		}
		n++
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", n, ts(max(c.Start, 0)), ts(c.End), strings.TrimSpace(c.Text))
	}
	return b.String()
}

// Cue is one subtitle line.
type Cue struct {
	Text       string
	Start, End float64
}
