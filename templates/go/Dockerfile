# __NAME__: Go, built in a throwaway stage; the image holds only the binary.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY . .
RUN go test ./... && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/app .

FROM alpine:3.22
RUN adduser -D -u 1000 app
USER app
COPY --from=build /out/app /app
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/app"]
