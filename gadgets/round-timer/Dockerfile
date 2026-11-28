# Round Timer: the single-file timer (round-timer.html, still the one copy in the repo, which also
# runs on its own from the Files app) plus the little server that keeps the shared session.
# Built on the Pi from a binary cross-compiled on the build box (tools/trinket deploy does it:
# GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o round-timer .), so the Pi compiles nothing.
FROM alpine:3.22
RUN mkdir /data && chown 1000:1000 /data
COPY round-timer /app
# The source, easy to find inside the container.
COPY go.mod *.go round-timer.html *.md /src/
COPY web /src/web
COPY tools /src/tools
EXPOSE 8080
HEALTHCHECK --interval=60s --timeout=3s CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/app"]
