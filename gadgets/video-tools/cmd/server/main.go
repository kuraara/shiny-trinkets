// Command server runs the video-tools web UI, JSON API and MCP endpoint.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/kuraara/clowd-docker-stuff/video-tools/internal/app"
	"github.com/kuraara/clowd-docker-stuff/video-tools/internal/config"
	"github.com/kuraara/clowd-docker-stuff/video-tools/internal/mcp"
	"github.com/kuraara/clowd-docker-stuff/video-tools/internal/storage"
	"github.com/kuraara/clowd-docker-stuff/video-tools/internal/web"
)

func main() {
	health := flag.Bool("healthcheck", false, "probe /healthz on the local server and exit 0/1 (for Docker HEALTHCHECK)")
	flag.Parse()
	cfg := config.Load()

	if *health {
		port := cfg.Addr[strings.LastIndex(cfg.Addr, ":")+1:]
		c := http.Client{Timeout: 4 * time.Second}
		resp, err := c.Get("http://127.0.0.1:" + port + "/healthz")
		if err != nil || resp.StatusCode != http.StatusOK {
			fmt.Fprintln(os.Stderr, "unhealthy:", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("video-tools ")
	svc, err := app.New(cfg)
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	svc.Start(ctx)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           web.New(svc, mcp.New(svc)),
		ReadHeaderTimeout: 20 * time.Second, // no overall read/write timeout: uploads and downloads can be large
		IdleTimeout:       2 * time.Minute,
	}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()
	log.Printf("version %s listening on %s; data %s (%s free); workers %d, ffmpeg threads %d; auth %v",
		app.Version, cfg.Addr, cfg.DataDir, storage.Human(storage.Free(cfg.DataDir)), cfg.Workers, cfg.FFmpegThreads,
		cfg.AuthToken != "")

	<-ctx.Done()
	log.Print("shutting down")
	svc.Jobs.Shutdown(8 * time.Second) // within Docker's default 10 s stop grace period
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(sctx)
}
