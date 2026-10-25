// Package media wraps ffprobe/ffmpeg: probing, a process runner with progress and hard
// cancellation, and the join (concatenate) engine.
package media

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// FF runs ffmpeg with bounded threads, progress reporting and process-group kill on cancel.
type FF struct {
	Threads int
	Log     func(string) // optional: receives ffmpeg's error output lines
}

// Run executes ffmpeg. onTime receives the output position in seconds as encoding proceeds.
func (f FF) Run(ctx context.Context, args []string, onTime func(sec float64)) error {
	full := append([]string{"-nostdin", "-hide_banner", "-v", "error", "-y", "-progress", "pipe:1", "-stats_period", "1"}, args...)
	cmd := exec.CommandContext(ctx, "ffmpeg", full...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("cannot start ffmpeg: %w", err)
	}
	tail := make(chan []string, 1)
	go func() {
		var lines []string
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			l := strings.TrimSpace(sc.Text())
			if l == "" {
				continue
			}
			if f.Log != nil {
				f.Log(l)
			}
			lines = append(lines, l)
			if len(lines) > 12 {
				lines = lines[1:]
			}
		}
		tail <- lines
	}()
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "out_time_us="); ok && onTime != nil {
			if us, err := strconv.ParseInt(v, 10, 64); err == nil && us >= 0 {
				onTime(float64(us) / 1e6)
			}
		}
	}
	err = cmd.Wait()
	lines := <-tail
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return fmt.Errorf("ffmpeg failed (%v): %s", err, strings.Join(lines, " | "))
	}
	return nil
}

// Stream is the subset of ffprobe stream data we use.
type Stream struct {
	Index        int               `json:"index"`
	CodecType    string            `json:"codec_type"`
	CodecName    string            `json:"codec_name"`
	Profile      string            `json:"profile"`
	PixFmt       string            `json:"pix_fmt"`
	Width        int               `json:"width"`
	Height       int               `json:"height"`
	SampleRate   string            `json:"sample_rate"`
	Channels     int               `json:"channels"`
	AvgFrameRate string            `json:"avg_frame_rate"`
	RFrameRate   string            `json:"r_frame_rate"`
	FieldOrder   string            `json:"field_order"`
	ColorTrans   string            `json:"color_transfer"`
	Disposition  map[string]int    `json:"disposition"`
	Tags         map[string]string `json:"tags"`
	SideData     []struct {
		Type     string  `json:"side_data_type"`
		Rotation float64 `json:"rotation"`
	} `json:"side_data_list"`
}

// Info describes one input clip.
type Info struct {
	Path     string  `json:"-"`
	Name     string  `json:"name"`
	Duration float64 `json:"duration"`
	Size     int64   `json:"size"`
	Video    *Stream `json:"-"`
	Audio    *Stream `json:"-"`
	Rotation int     `json:"rotation"` // 0/90/180/270, from the display matrix
	DispW    int     `json:"width"`    // as displayed (after rotation)
	DispH    int     `json:"height"`
	FPS      float64 `json:"fps"`
	VCodec   string  `json:"video_codec"`
	ACodec   string  `json:"audio_codec"`
}

// Probe reads a clip's streams with ffprobe.
func Probe(ctx context.Context, path string) (Info, error) {
	out, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_format", "-show_streams",
		"-of", "json", path).Output()
	if err != nil {
		msg := ""
		if ee, ok := err.(*exec.ExitError); ok {
			msg = strings.TrimSpace(string(ee.Stderr))
		}
		return Info{}, fmt.Errorf("can't read %s as a video: %s", path, msg)
	}
	var p struct {
		Streams []Stream `json:"streams"`
		Format  struct {
			Duration string `json:"duration"`
			Size     string `json:"size"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &p); err != nil {
		return Info{}, err
	}
	inf := Info{Path: path}
	inf.Duration, _ = strconv.ParseFloat(p.Format.Duration, 64)
	inf.Size, _ = strconv.ParseInt(p.Format.Size, 10, 64)
	for i := range p.Streams {
		s := &p.Streams[i]
		switch {
		case s.CodecType == "video" && s.Disposition["attached_pic"] == 0 && inf.Video == nil:
			inf.Video = s
		case s.CodecType == "audio" && inf.Audio == nil:
			inf.Audio = s
		}
	}
	if inf.Video == nil {
		return Info{}, fmt.Errorf("%s has no video stream", path)
	}
	if inf.Duration <= 0 {
		return Info{}, fmt.Errorf("%s has no readable duration", path)
	}
	v := inf.Video
	inf.VCodec = v.CodecName
	if inf.Audio != nil {
		inf.ACodec = inf.Audio.CodecName
	}
	rot := 0.0
	for _, sd := range v.SideData {
		if sd.Type == "Display Matrix" {
			rot = sd.Rotation
		}
	}
	if r, err := strconv.ParseFloat(v.Tags["rotate"], 64); err == nil && rot == 0 {
		rot = r
	}
	inf.Rotation = ((int(math.Round(rot)) % 360) + 360) % 360
	inf.DispW, inf.DispH = v.Width, v.Height
	if inf.Rotation == 90 || inf.Rotation == 270 {
		inf.DispW, inf.DispH = v.Height, v.Width
	}
	inf.FPS = rate(v.AvgFrameRate)
	if inf.FPS <= 0 || inf.FPS > 1000 {
		inf.FPS = rate(v.RFrameRate)
	}
	return inf, nil
}

func rate(s string) float64 {
	n, d, ok := strings.Cut(s, "/")
	if !ok {
		f, _ := strconv.ParseFloat(s, 64)
		return f
	}
	a, _ := strconv.ParseFloat(n, 64)
	b, _ := strconv.ParseFloat(d, 64)
	if b == 0 {
		return 0
	}
	return a / b
}
