# Subtitle Writer. Built on the Pi from a binary cross-compiled on the build box (tools/trinket
# deploy does it: GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o subtitle-writer .), so the Pi
# compiles nothing. Runs as the media files' owner (uid/gid from the compose file).
FROM alpine:3.22
RUN mkdir /data && chown 1000:1001 /data
COPY subtitle-writer /app
COPY go.mod *.go /src/
EXPOSE 8080
HEALTHCHECK --interval=60s --timeout=3s CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/app"]
