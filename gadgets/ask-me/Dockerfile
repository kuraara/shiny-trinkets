# Ask Me. Built on the Pi from a binary cross-compiled on the build box (tools/trinket deploy
# does it: GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o ask-me .), so the Pi compiles nothing.
FROM alpine:3.22
RUN adduser -D -u 1000 app && mkdir /data && chown app /data
COPY ask-me /app
# The source, easy to find inside the container.
COPY go.mod *.go /src/
COPY web /src/web
USER app
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/app"]
