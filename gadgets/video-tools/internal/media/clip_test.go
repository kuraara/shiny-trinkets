package media

import (
	"context"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestClipIntegration(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg")
	}
	dir := t.TempDir()
	// 8 s, 1280x720 with a keyframe every 2 s, plus audio, in Matroska (like a library file)
	src := gen(t, dir, "src.mkv", "-f", "lavfi", "-i", "testsrc2=size=1280x720:rate=25", "-f", "lavfi", "-i", "sine=frequency=300",
		"-t", "8", "-c:v", "libx264", "-g", "50", "-c:a", "aac")
	in := probeAll(t, src)[0]
	fonts, _ := filepath.Abs("../../assets/fonts")
	srt := filepath.Join(dir, "subs.srt")
	os.WriteFile(srt, []byte(SRT([]Cue{{"Hello there.", 0.2, 1.2}, {"Second line", 1.4, 2.4}, {"", 0, 1}})), 0o644)
	ff := FF{Threads: 2}

	acc := filepath.Join(dir, "acc"+ClipExt(in, ClipOptions{Mode: "accurate"}))
	o := ClipOptions{Start: 3.3, End: 5.8, Mode: "accurate", MaxHeight: 480, Subtitles: srt, FontsDir: fonts}
	if err := ff.Run(context.Background(), ClipArgs(in, o, 2, acc), nil); err != nil {
		t.Fatal(err)
	}
	got := probeAll(t, acc)[0]
	if math.Abs(got.Duration-2.5) > 0.12 || got.DispH != 480 || got.DispW != 854 || got.VCodec != "h264" || got.ACodec != "aac" {
		t.Fatalf("accurate clip: %+v", got)
	}

	fast := filepath.Join(dir, "fast"+ClipExt(in, ClipOptions{Mode: "fast"}))
	if filepath.Ext(fast) != ".mp4" { // H.264/AAC copies straight into MP4
		t.Fatalf("fast ext %s", fast)
	}
	if err := ff.Run(context.Background(), ClipArgs(in, ClipOptions{Start: 3.3, End: 5.8, Mode: "fast"}, 2, fast), nil); err != nil {
		t.Fatal(err)
	}
	// starts on the keyframe at 2.0 s, so it's longer than asked
	if d := probeAll(t, fast)[0].Duration; d < 2.5 || d > 4.0 {
		t.Fatalf("fast clip duration %.2f", d)
	}
}

func TestSRT(t *testing.T) {
	got := SRT([]Cue{{"A", -0.5, 1.25}, {"  ", 1, 2}, {"B", 3661.5, 3662}})
	want := "1\n00:00:00,000 --> 00:00:01,250\nA\n\n2\n01:01:01,500 --> 01:01:02,000\nB\n\n"
	if got != want {
		t.Fatalf("got %q", got)
	}
}
