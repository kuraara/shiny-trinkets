# Secret Drop. Built on the Pi from a binary cross-compiled on the build box (tools/trinket deploy
# does it), so the Pi compiles nothing. Runs as Claude's user (set in the compose file) so the files
# it writes are Claude's, mode 0600.
FROM alpine:3.22
COPY secret-drop /app
# The source, easy to find inside the container.
COPY go.mod *.go /src/
COPY web /src/web
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/app"]
