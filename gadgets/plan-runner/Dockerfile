# Plan Runner: the Go server plus what a plan usually needs (git, Go, Node, Python, build tools)
# and both agent CLIs; EXECUTOR picks which one runs (claude, codex or command).
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod *.go ./
COPY web ./web
RUN go vet ./... && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/plan-runner .

FROM node:22-bookworm
RUN apt-get update -qq && apt-get install -y -qq --no-install-recommends python3 python3-venv python3-pip ripgrep jq \
 && rm -rf /var/lib/apt/lists/* \
 && npm install -g @anthropic-ai/claude-code @openai/codex && npm cache clean --force
COPY --from=build /usr/local/go /usr/local/go
ENV PATH=/usr/local/go/bin:/home/node/go/bin:$PATH DATA=/data
COPY --from=build /out/plan-runner /usr/local/bin/plan-runner
# The source, easy to find inside the container.
COPY go.mod *.go /src/
COPY web /src/web
RUN mkdir /data && chown node:node /data
USER node
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s CMD wget -qO- http://127.0.0.1:8080/healthz || curl -fs http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["plan-runner"]
