// Package config reads all runtime settings from environment variables.
// Every knob has a safe default so the container runs with no configuration.
package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr       string // listen address
	PublicURL  string // external base URL, used for absolute links handed to agents (MCP)
	AuthToken  string // optional shared secret; empty = no auth
	DataDir    string // root of all writable state (a volume)
	ModelsDir  string // speech models (downloaded on first start)
	InboxDir   string // optional read-only drop folder for inputs
	WorkerDir  string // python worker scripts
	FontsDir   string // subtitle fonts
	SourceDir  string // this project's source, shipped inside the image
	Python     string // python interpreter
	SetupModel string // script that downloads the speech models

	MaxUploadBytes    int64         // per HTTP request (all files together)
	MinFreeBytes      int64         // never let a job take the volume below this
	JobTTL            time.Duration // finished jobs and unused uploads are deleted after this
	Workers           int           // concurrent jobs (each video job is one ffmpeg at a time)
	FFmpegThreads     int           // -threads for decode/encode; bounds CPU and RAM per job
	ASRThreads        int           // speech model threads
	MaxTranscriptSecs int           // longest clip accepted for a panel transcript
	MaxJoinFiles      int           // most clips in one join
	JoinMaxHeight     int           // re-encoded joins are scaled down to this height
	KeepJoinInputs    bool          // keep uploaded clips after a successful join
}

func Load() Config {
	data := env("DATA_DIR", "/data")
	return Config{
		Addr:       env("LISTEN_ADDR", ":8080"),
		PublicURL:  strings.TrimRight(env("PUBLIC_URL", ""), "/"),
		AuthToken:  env("AUTH_TOKEN", ""),
		DataDir:    data,
		ModelsDir:  env("MODELS_DIR", filepath.Join(data, "models")),
		InboxDir:   env("INBOX_DIR", filepath.Join(data, "inbox")),
		WorkerDir:  env("WORKER_DIR", "/app/worker"),
		FontsDir:   env("FONTS_DIR", "/app/assets/fonts"),
		SourceDir:  env("SOURCE_DIR", "/app/src"),
		Python:     env("PYTHON", "python3"),
		SetupModel: env("SETUP_MODELS_SCRIPT", "/app/scripts/setup-models.sh"),

		MaxUploadBytes:    int64(envInt("MAX_UPLOAD_MB", 8192)) << 20,
		MinFreeBytes:      int64(envInt("MIN_FREE_MB", 2048)) << 20,
		JobTTL:            time.Duration(envInt("JOB_TTL_HOURS", 24)) * time.Hour,
		Workers:           max(1, envInt("WORKERS", 1)),
		FFmpegThreads:     max(1, envInt("FFMPEG_THREADS", 2)),
		ASRThreads:        max(1, envInt("ASR_THREADS", 2)),
		MaxTranscriptSecs: envInt("MAX_TRANSCRIPT_SECONDS", 600),
		MaxJoinFiles:      envInt("MAX_JOIN_FILES", 20),
		JoinMaxHeight:     envInt("JOIN_MAX_HEIGHT", 2160),
		KeepJoinInputs:    envBool("KEEP_JOIN_INPUTS", false),
	}
}

func env(k, def string) string {
	if v, ok := os.LookupEnv(k); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func envInt(k string, def int) int {
	if n, err := strconv.Atoi(env(k, "")); err == nil {
		return n
	}
	return def
}

func envBool(k string, def bool) bool {
	if b, err := strconv.ParseBool(env(k, "")); err == nil {
		return b
	}
	return def
}
