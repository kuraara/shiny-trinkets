# Disk Space scanner. Self-contained: builds and tests the Go program, checks a real snapshot
# opens in ncdu, then copies the binary into a small image. Builds on any arm64 or amd64 Docker
# host (the build box, or another Pi while the PC is off):  docker build -t disk-scan:latest .

# 1. compile, vet, test
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod *.go ./
RUN go vet ./... && go test ./... \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/diskscan .

# 2. a snapshot of this image's /usr must import into ncdu (unknown keys like "other" included)
FROM alpine:3.22 AS verify
RUN apk add --no-cache ncdu
COPY --from=build /out/diskscan /usr/local/bin/diskscan
RUN diskscan scan -root /usr -name /usr -keep 3 -min-mb 1 -o /tmp/snap.json 2>/tmp/stats.json \
 && ncdu -f /tmp/snap.json -o /tmp/roundtrip.json \
 && grep -q '"name":"/usr"' /tmp/roundtrip.json && cat /tmp/stats.json && touch /ok

# 3. the image
FROM alpine:3.22
RUN apk add --no-cache tzdata util-linux-misc
COPY --from=verify /ok /usr/local/share/diskscan-verified
COPY --from=build /out/diskscan /usr/local/bin/diskscan
# The source, easy to find inside the container.
COPY go.mod *.go /src/
HEALTHCHECK --interval=60s --timeout=5s CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1
# Lowest disk (idle class) and CPU priority, for the whole process and every thread.
ENTRYPOINT ["ionice", "-c3", "nice", "-n19", "diskscan", "serve"]
