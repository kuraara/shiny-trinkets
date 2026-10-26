# video-tools: panel transcripts + clip joining (Go web UI / API / MCP, Python speech worker, ffmpeg).
#
# Build and run with the docker-compose.yml next to this file (Portainer: Stacks -> Add stack ->
# Repository, compose path video-tools/docker-compose.yml). Speech models (~430 MB) are NOT baked in;
# they download into the /data volume on first start, so rebuilding the image never re-downloads them.
#
# Built and smoke-tested end to end on Docker 27 (amd64) through the API and MCP: joins, panel
# transcripts, cancel and restart. The image is about 780 MB. The runtime base is Debian bookworm, whose ffmpeg is 5.1; the code and tests also pass on 7.1
# (trixie), so moving the base image forward is fine.

ARG GO_VERSION=1.24
ARG PYTHON_VERSION=3.12

# --- build the Go server ---------------------------------------------------------------------
FROM golang:${GO_VERSION}-bookworm AS build
WORKDIR /src
# templ generator pinned to the same version as the runtime in go.mod
RUN go install github.com/a-h/templ/cmd/templ@v0.3.1001
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN templ generate \
 && CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X github.com/kuraara/shiny-trinkets/gadgets/video-tools/internal/app.Version=${VERSION}" \
      -o /out/video-tools ./cmd/server

# --- runtime ---------------------------------------------------------------------------------
FROM python:${PYTHON_VERSION}-slim-bookworm
# ffmpeg: all video work. curl + bzip2: first-start model download.
RUN apt-get update \
 && apt-get install -y --no-install-recommends ffmpeg curl bzip2 ca-certificates \
 && rm -rf /var/lib/apt/lists/*
COPY worker/requirements.txt /app/worker/requirements.txt
RUN pip install --no-cache-dir -r /app/worker/requirements.txt

COPY --from=build /out/video-tools /usr/local/bin/video-tools
COPY worker  /app/worker
COPY assets  /app/assets
COPY scripts /app/scripts
# the complete source, as built, for reference and for agents (also served at /source.tar.gz)
COPY . /app/src

# runs as an unprivileged user; /data is created owned by it so a new named volume inherits that
RUN useradd --uid 1000 --create-home --shell /usr/sbin/nologin app \
 && mkdir -p /data/models /data/inbox /data/tmp \
 && chown -R app:app /data \
 && chmod +x /app/scripts/*.sh
USER app

ENV DATA_DIR=/data \
    WORKER_DIR=/app/worker \
    FONTS_DIR=/app/assets/fonts \
    SOURCE_DIR=/app/src \
    SETUP_MODELS_SCRIPT=/app/scripts/setup-models.sh \
    LISTEN_ADDR=:8080 \
    TMPDIR=/data/tmp \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1

VOLUME ["/data"]
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 CMD ["video-tools", "-healthcheck"]
ENTRYPOINT ["video-tools"]
