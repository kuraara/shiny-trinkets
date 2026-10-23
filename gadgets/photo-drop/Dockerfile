FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /photo-drop .

FROM alpine:3.22
RUN apk add --no-cache ca-certificates && adduser -D -H -u 1000 app && mkdir -p /data && chown app /data
COPY --from=build /photo-drop /usr/local/bin/photo-drop
USER app
ENV DATA_DIR=/data ADDR=:8080
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["photo-drop"]
