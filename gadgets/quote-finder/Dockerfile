FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
COPY web ./web
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /quote-finder .

FROM alpine:3.22
RUN adduser -D -H -u 1000 app && mkdir -p /data && chown app /data
COPY --from=build /quote-finder /usr/local/bin/quote-finder
USER app
ENV DATA_DIR=/data ADDR=:8080
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["quote-finder"]
