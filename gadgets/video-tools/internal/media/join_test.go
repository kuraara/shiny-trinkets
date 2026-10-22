package media

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func info(codec, profile string, w, h, rot int, fps float64, audio bool, dur float64) Info {
	in := Info{Duration: dur, VCodec: codec, Rotation: rot, DispW: w, DispH: h, FPS: fps, Size: 1 << 20,
		Video: &Stream{CodecName: codec, Profile: profile, Width: w, Height: h, PixFmt: "yuv420p"}}
	if rot == 90 || rot == 270 {
		in.DispW, in.DispH = h, w
	}
	if audio {
		in.Audio = &Stream{CodecName: "aac", SampleRate: "48000", Channels: 2}
		in.ACodec = "aac"
	}
	return in
}

func TestPlanCopyWhenCompatible(t *testing.T) {
	a := info("h264", "High", 1920, 1080, 0, 30, true, 10)
	p, err := PlanJoin([]Info{a, a}, JoinOptions{Mode: "auto"})
	if err != nil || !p.Copy || p.Ext != ".mp4" || p.Duration != 20 {
		t.Fatalf("want stream copy to mp4, got %+v err %v", p, err)
	}
}

func TestPlanFallsBackWithReasons(t *testing.T) {
	a := info("h264", "High", 1920, 1080, 0, 30, true, 10)
	b := info("hevc", "Main", 1280, 720, 90, 120, false, 5)
	p, err := PlanJoin([]Info{a, b}, JoinOptions{Mode: "auto"})
	if err != nil || p.Copy {
		t.Fatalf("want re-encode, got %+v err %v", p, err)
	}
	why := strings.Join(p.Why, ";")
	for _, want := range []string{"hevc", "1280x720", "rotated", "sound"} {
		if !strings.Contains(why, want) {
			t.Errorf("reasons %q missing %q", why, want)
		}
	}
	if p.Target.W != 1920 || p.Target.H != 1080 || p.Target.FPS != "30" {
		t.Errorf("target %+v", p.Target)
	}
	if _, err := PlanJoin([]Info{a, b}, JoinOptions{Mode: "copy"}); err == nil {
		t.Error("copy-only mode should refuse incompatible clips")
	}
}

func TestPlanTargetAndClamp(t *testing.T) {
	phone := info("hevc", "Main", 3840, 2160, 90, 59.7, true, 1.2) // portrait 4K, VFR-ish
	b := info("h264", "High", 1920, 1080, 0, 25, true, 8)
	p, err := PlanJoin([]Info{phone, b}, JoinOptions{Transition: "crossfade", Seconds: 2, MaxHeight: 1920})
	if err != nil {
		t.Fatal(err)
	}
	if p.Target.H != 1920 || p.Target.W != 1080 || p.Target.FPS != "60000/1001" {
		t.Errorf("target %+v", p.Target)
	}
	if math.Abs(p.Seconds-0.4) > 1e-9 { // clamped to a third of the shortest clip
		t.Errorf("seconds %v", p.Seconds)
	}
	if math.Abs(p.Duration-(9.2-0.4)) > 1e-9 {
		t.Errorf("duration %v", p.Duration)
	}
	if fps, _ := snapFPS(120); fps != "60" {
		t.Errorf("120 fps should cap at 60, got %s", fps)
	}
}

// --- integration: real ffmpeg on generated clips -------------------------------------------

func gen(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	out := filepath.Join(dir, name)
	cmd := exec.Command("ffmpeg", append(append([]string{"-v", "error", "-y"}, args...), out)...)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("gen %s: %v %s", name, err, b)
	}
	return out
}

// genRotated makes a clip with gen, then remuxes it with a 90° display matrix, like a phone
// recording held upright. ffmpeg 6.0+ sets the matrix with -display_rotation; older versions
// (Debian bookworm's 5.1, which the image uses) only accept the "rotate" metadata tag. Try
// each and keep the first that ffprobe reports as rotated.
func genRotated(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	plain := gen(t, dir, "plain_"+name, args...)
	out := filepath.Join(dir, name)
	for _, a := range [][]string{
		{"-display_rotation", "90", "-i", plain, "-c", "copy"},
		{"-i", plain, "-c", "copy", "-metadata:s:v:0", "rotate=90"},
	} {
		cmd := exec.Command("ffmpeg", append(append([]string{"-v", "error", "-y"}, a...), out)...)
		if cmd.Run() != nil {
			continue
		}
		if r := probeAll(t, out)[0].Rotation; r == 90 || r == 270 {
			return out
		}
	}
	t.Fatalf("gen %s: this ffmpeg wouldn't write a rotated clip", name)
	return ""
}

func probeAll(t *testing.T, paths ...string) []Info {
	var in []Info
	for _, p := range paths {
		i, err := Probe(context.Background(), p)
		if err != nil {
			t.Fatal(err)
		}
		in = append(in, i)
	}
	return in
}

func streamDur(t *testing.T, path, sel string) float64 {
	out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", sel, "-count_packets",
		"-show_entries", "stream=duration,codec_name", "-of", "csv=p=0", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	var f float64
	parts := strings.Split(strings.TrimSpace(string(out)), ",")
	for _, p := range parts {
		if _, err := fmt.Sscan(p, &f); err == nil {
			return f
		}
	}
	return f
}

func TestJoinIntegration(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	if d := os.Getenv("JOIN_TEST_DIR"); d != "" { // keep outputs for inspection
		dir = d
		_ = os.MkdirAll(dir, 0o755)
	}
	a := gen(t, dir, "a.mp4", "-f", "lavfi", "-i", "testsrc2=s=1280x720:r=30:d=4", "-f", "lavfi", "-i",
		"sine=f=440:d=4", "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest")
	b := gen(t, dir, "b.mp4", "-f", "lavfi", "-i", "smptebars=s=1280x720:r=30:d=3", "-f", "lavfi", "-i",
		"sine=f=660:d=3", "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest")
	// portrait phone-style clip: landscape pixels + a 90° display matrix, no audio track
	c := genRotated(t, dir, "c.mp4", "-f", "lavfi", "-i", "mandelbrot=s=640x360:r=25",
		"-t", "3", "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p")
	inputs := []string{a, b, c}
	if u := os.Getenv("JOIN_TEST_EXTRA"); u != "" { // e.g. an iPhone HEVC screen recording
		inputs = append(inputs, u)
	}
	ff := FF{Threads: 2}
	cases := []struct {
		name  string
		clips []string
		o     JoinOptions
		copy  bool
	}{
		{"copy", []string{a, b}, JoinOptions{Mode: "auto"}, true},
		{"mixed_none", inputs, JoinOptions{Mode: "auto"}, false},
		{"fade", inputs, JoinOptions{Transition: "fade", Seconds: 0.5}, false},
		{"crossfade", inputs, JoinOptions{Transition: "crossfade", Seconds: 0.75, MaxHeight: 720}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := probeAll(t, tc.clips...)
			p, err := PlanJoin(in, tc.o)
			if err != nil {
				t.Fatal(err)
			}
			if p.Copy != tc.copy {
				t.Fatalf("copy=%v, want %v (why: %v)", p.Copy, tc.copy, p.Why)
			}
			work := filepath.Join(dir, tc.name+"_work")
			_ = os.MkdirAll(work, 0o755)
			out := filepath.Join(dir, "out_"+tc.name+p.Ext)
			last := -1
			err = Join(context.Background(), in, tc.o, p, work, out, ff, func(pct int, msg string) {
				if pct < last {
					t.Errorf("progress went backwards: %d after %d (%s)", pct, last, msg)
				}
				last = pct
			})
			if err != nil {
				t.Fatal(err)
			}
			got := probeAll(t, out)[0]
			if math.Abs(got.Duration-p.Duration) > 0.15 {
				t.Errorf("duration %.3f, planned %.3f", got.Duration, p.Duration)
			}
			vd, ad := streamDur(t, out, "v:0"), streamDur(t, out, "a:0")
			if math.Abs(vd-ad) > 0.1 {
				t.Errorf("audio/video length differ: video %.3f audio %.3f", vd, ad)
			}
			if !tc.copy && (got.DispW != p.Target.W || got.DispH != p.Target.H) {
				t.Errorf("output %dx%d, want %dx%d", got.DispW, got.DispH, p.Target.W, p.Target.H)
			}
			left, _ := os.ReadDir(work)
			for _, e := range left {
				if strings.HasSuffix(e.Name(), ".mov") {
					t.Errorf("intermediate %s not cleaned up", e.Name())
				}
			}
			var ru syscall.Rusage
			_ = syscall.Getrusage(syscall.RUSAGE_CHILDREN, &ru)
			t.Logf("%s: copy=%v %dx%d@%s %.2fs (planned %.2fs) seconds=%.2f peak child RSS %d MB why=%v",
				tc.name, p.Copy, got.DispW, got.DispH, p.Target.FPS, got.Duration, p.Duration, p.Seconds, ru.Maxrss/1024, p.Why)
		})
	}
}
