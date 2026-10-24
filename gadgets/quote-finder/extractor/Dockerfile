# Built on the Pi from a binary cross-compiled on the build box (see README), so the Pi
# never compiles anything: GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o extractor .
FROM alpine:3.22
RUN apk add --no-cache ffmpeg util-linux-misc
COPY extractor /usr/local/bin/extractor
ENTRYPOINT ["extractor"]
