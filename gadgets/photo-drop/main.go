// Photo Drop: guests scan a QR code, send photos, and the host approves each one before it
// lands on a shared "desk" display.
package main

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

//go:embed web
var webFiles embed.FS

type config struct {
	Addr           string
	DataDir        string
	PublicURL      string // e.g. https://drop.example.com; empty = work it out from each request
	CreatePIN      string // if set, starting a session needs this PIN
	RoomTTL        time.Duration
	MaxRooms       int
	MaxPhotos      int // per room
	MaxPeople      int // per room
	MaxPerPerson   int // photos one guest can send in total
	MaxPendingEach int // photos one guest can have waiting for approval
	MaxUploadBytes int64
}

func envStr(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(k))); err == nil && v > 0 {
		return v
	}
	return def
}

func loadConfig() config {
	return config{
		Addr:           envStr("ADDR", ":8080"),
		DataDir:        envStr("DATA_DIR", "/data"),
		PublicURL:      strings.TrimRight(envStr("PUBLIC_URL", ""), "/"),
		CreatePIN:      envStr("CREATE_PIN", ""),
		RoomTTL:        time.Duration(envInt("ROOM_TTL_HOURS", 48)) * time.Hour,
		MaxRooms:       envInt("MAX_ROOMS", 50),
		MaxPhotos:      envInt("MAX_PHOTOS_PER_ROOM", 1000),
		MaxPeople:      envInt("MAX_PEOPLE_PER_ROOM", 500),
		MaxPerPerson:   envInt("MAX_PHOTOS_PER_PERSON", 50),
		MaxPendingEach: envInt("MAX_PENDING_PER_PERSON", 10),
		MaxUploadBytes: int64(envInt("MAX_UPLOAD_MB", 15)) << 20,
	}
}

func main() {
	cfg := loadConfig()
	st, err := openStore(cfg)
	if err != nil {
		log.Fatal(err)
	}
	web, err := fs.Sub(webFiles, "web")
	if err != nil {
		log.Fatal(err)
	}
	a := &app{cfg: cfg, store: st, web: web}
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           a.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go st.janitor(ctx)
	go func() {
		<-ctx.Done()
		st.shutdown() // ends live streams so Shutdown doesn't wait on them
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()

	log.Printf("photo-drop listening on %s, data in %s, %d session(s) loaded", cfg.Addr, cfg.DataDir, st.count())
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	st.saveDirty()
}
