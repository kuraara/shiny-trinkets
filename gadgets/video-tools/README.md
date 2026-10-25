# video-tools

A small self-hosted web app (plus JSON API and MCP endpoint for agents) that does three things with
video clips:

- **Panel transcript** — pulls stills from a clip, transcribes the dialogue, and stacks the stills
  vertically on black with each line burned in as a subtitle, `Name: “line”`. You pick the moments
  (e.g. `1.0, 2.5, 7.2`) or let it take one per line of dialogue, then name the speakers on a
  review screen that previews the finished image live.
- **Join clips** — joins clips end to end. Matching clips with no transition are joined **without
  re-encoding** (stream copy: lossless, seconds). Fades through black, crossfades or mismatched
  clips are converted to one common H.264/AAC format first, and the result says why.
- **Cut a clip** — cuts a stretch out of a longer video. *Exact* gives a frame-accurate H.264/AAC
  MP4 that plays anywhere (up to 1080p; HDR is tone-mapped so colours look right; subtitles can
  be burned in through the API). *Fast copy* keeps the original quality and takes seconds, but
  starts on the nearest keyframe.

Quote Finder sends scenes here: panel scripts arrive with the subtitle lines already filled in
(`lines` on `/api/panel` skips speech recognition), and clips arrive through `/api/clip`.

Go (templ + htmx) for the web app, a Python worker for speech (Whisper via sherpa-onnx for text,
Vosk for word timings, Praat for voice pitch) and rendering, ffmpeg for everything video.

## Deploy with Portainer

1. **Stacks → Add stack → Repository**
   - Repository URL: `https://github.com/kuraara/clowd-docker-stuff`
   - Reference: `refs/heads/master`
   - Compose path: `video-tools/docker-compose.yml`
2. Optional **environment variables** (see [Configuration](#configuration)); the useful ones are
   `PUBLIC_URL` (e.g. `http://<pc>:8090`), `AUTH_TOKEN` and `VIDEO_TOOLS_PORT` (default 8090).
3. Deploy, then open `http://<host>:8090`.

The compose file joins the gadget registry's `gadgets` network so Quote Finder can reach it as
`http://video-tools:8080`; without the registry, create it first with `docker network create gadgets`.

On first start the speech models (~430 MB) download into the data volume in the background. Joining
works immediately; panel transcripts wait for the download (progress is on the **Status** page).
Rebuilding or updating the image never re-downloads them.

To update: push to the branch, then **Pull and redeploy** in Portainer (tick *Re-pull image and
redeploy* so it rebuilds).

Without Portainer: `cd video-tools && docker compose up -d --build`.

## Using it

**Panel transcript** — upload a clip (or pick one from the inbox), choose *one panel per line of
dialogue* or enter times. After analysis (roughly 5× the clip length on two CPU cores; ~50 s for a
9 s clip) the review screen shows the panels as they'll look and, for each line, a strip of eight
frames across that line plus the voice pitch:

- whoever's mouth moves across the strip is the speaker;
- pitch is a hint: children ~300–500 Hz, adult women ~165–300 Hz, adult men ~85–180 Hz. Lines where
  pitch is suspiciously steady are flagged (usually music or a sound effect).

Type the names (suggestions fill in as you go), adjust wording or which panel a line sits on, and
**Render image**. Downloads: the PNG, `transcript.md` and `transcript.json`. You can re-render as
often as you like.

**Join clips** — add clips (drag to the drop zone, or add ones already on the server), order them,
choose *None*, *Fade through black* or *Crossfade* and a length. *Encoding* lets you force stream
copy (fails rather than re-encode) or always re-encode.

## Agents (MCP)

The container serves MCP over streamable HTTP at `/mcp`. Its instructions and tool descriptions
carry the whole workflow, including how to identify speakers (mouth movement first, pitch as a
hint, never identify a real person from their face).

```sh
# Claude Code (add --header "Authorization: Bearer <AUTH_TOKEN>" if you set a token)
claude mcp add --transport http video-tools http://<pc>:8090/mcp
```

Tools: `list_clips`, `start_panel_transcript`, `get_job`, `get_panel_images`,
`render_panel_transcript`, `join_clips`, `cut_clip`, `cancel_job`. Agents get clips in with
`curl -F file=@clip.mov http://<pc>:8090/api/files` or from the inbox folder. Analysis results
come back with the frames and per-line contact sheets as images, so the agent can name the speakers
itself.

Claude's apps (claude.ai / Desktop "connectors") need an internet-reachable HTTPS URL; on a LAN use
Claude Code or any MCP client that can reach the host. If you expose it beyond your LAN, set
`AUTH_TOKEN`.

The same operations are available as a JSON API — `GET /api` lists the endpoints.

## Keeping storage and memory in check

| Risk | What stops it |
|---|---|
| Uploads filling RAM | Uploads stream straight to disk in 1 MB chunks; nothing is buffered in memory. |
| Uploads filling the disk | `MAX_UPLOAD_MB` per request, and a `MIN_FREE_MB` reserve checked before and every 64 MB during each upload. |
| Jobs filling the disk | Each join estimates its peak disk use (intermediates + output) and refuses to start if it would break the reserve. Intermediates are deleted as soon as the output is written, and a job's `work/` folder is always removed when the job ends — success, failure or cancel. `+faststart` (which rewrites the file once more) is skipped automatically if space is tight. |
| Old files piling up | Finished jobs and unused uploads are deleted after `JOB_TTL_HOURS`; uploaded clips are deleted right after a successful join unless `KEEP_JOIN_INPUTS=true`. Inbox files are never touched. |
| Memory / CPU spikes | One job at a time by default (`WORKERS`), ffmpeg capped at `FFMPEG_THREADS`, and re-encoded joins work one segment at a time (at most two short inputs decoded at once), so memory stays flat however many clips you join. Measured peaks: ffmpeg 90–240 MB for 720p–1320p joins; panel analysis ~1 GB (speech models). The stack sets `mem_limit: 3g`, `cpus: 2`. |
| Huge outputs | Re-encoded joins are scaled down to `JOIN_MAX_HEIGHT`; frame rates above 60 are capped at 60. |
| Runaway / orphan processes | ffmpeg and the worker run in their own process groups and are killed as a group on cancel or shutdown; shutdown waits for them (8 s). Jobs interrupted by a restart are marked failed, never half-resumed. |

## Configuration

All optional; set them in the Portainer stack.

| Variable | Default | Meaning |
|---|---|---|
| `VIDEO_TOOLS_PORT` | `8090` | Host port. |
| `PUBLIC_URL` | (from request) | External base URL used in links given to agents. |
| `AUTH_TOKEN` | (none) | If set: UI sign-in, and `Authorization: Bearer` for API/MCP. |
| `MAX_UPLOAD_MB` | `8192` | Largest upload request (all files together). |
| `MIN_FREE_MB` | `2048` | Free space always kept on the data volume. |
| `JOB_TTL_HOURS` | `24` | Finished jobs and unused uploads are deleted after this. |
| `KEEP_JOIN_INPUTS` | `false` | Keep uploaded clips after they've been joined. |
| `WORKERS` | `1` | Jobs at once (raise `mem_limit` too). |
| `FFMPEG_THREADS` | `2` | ffmpeg decode/encode threads per job. |
| `ASR_THREADS` | `2` | Speech model threads. |
| `MAX_TRANSCRIPT_SECONDS` | `600` | Longest clip accepted for a panel transcript. |
| `MAX_JOIN_FILES` | `20` | Most clips in one join. |
| `JOIN_MAX_HEIGHT` | `2160` | Re-encoded joins are scaled down to this height. |

The data volume holds `files/` (uploads), `jobs/<id>/{out,work}`, `models/`, `tmp/` and
optionally `inbox/` (bind-mount a folder read-only to use clips without uploading — see the compose
file).

## Source

The complete source ships inside the image at `/app/src` and can be downloaded from the running
app at `/source.tar.gz` (footer link).

```
cmd/server/          main: HTTP server, -healthcheck flag for Docker
internal/app/        service layer shared by UI, API and MCP (job runners)
internal/jobs/       queue, per-job folders, cancel, restart recovery, janitor
internal/storage/    streamed uploads, free-space reserve, inbox
internal/media/      ffprobe/ffmpeg runner, join planner + engine (+ tests)
internal/panel/      calls the Python worker, review/render, transcript export
internal/models/     first-start model download
internal/mcp/        MCP endpoint (standard library only)
internal/web/        handlers, auth, JSON API; views/*.templ
worker/              analyse.py, render.py, requirements.txt
scripts/             setup-models.sh
web/static/          CSS, JS, htmx (vendored)
assets/fonts/        Inter (subtitles and UI)
```

### Develop locally

```sh
pip install -r worker/requirements.txt              # Python 3.12+, plus ffmpeg on PATH
MODELS_DIR=$PWD/data/models bash scripts/setup-models.sh
go install github.com/a-h/templ/cmd/templ@v0.3.1001 && templ generate
go build -o bin/video-tools ./cmd/server
DATA_DIR=$PWD/data WORKER_DIR=$PWD/worker FONTS_DIR=$PWD/assets/fonts SOURCE_DIR=$PWD \
  SETUP_MODELS_SCRIPT=$PWD/scripts/setup-models.sh LISTEN_ADDR=:8090 ./bin/video-tools
go test ./...                                      # join engine tests run real ffmpeg
```

### Notes

- The image has been built and smoke-tested end to end through the API and MCP (joins, panel
  transcripts, cancel and restart). `go test ./...` passes with ffmpeg 5.1 (the image's) and 7.1.
- A panel transcript peaked at about 1.5 GB of container memory (file cache included); ffmpeg
  during joins stays under about 300 MB.
- Speech models are English-only.
- Licences: Inter (OFL 1.1), htmx (0BSD), Whisper weights (MIT), sherpa-onnx and Vosk (Apache 2.0),
  Praat/parselmouth (GPL 3.0) — fine for self-hosting; check before redistributing the image.
