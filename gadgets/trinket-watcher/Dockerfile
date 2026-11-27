FROM python:3.12-alpine
RUN apk add --no-cache git openssh-client tar
COPY watcher.py entrypoint.sh /app/
COPY web /app/web
COPY . /src
ENTRYPOINT ["/app/entrypoint.sh"]
