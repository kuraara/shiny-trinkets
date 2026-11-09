# Round Timer: the single-file timer (round-timer.html) served unchanged by nginx, as "/".
# round-timer.html stays the one copy in the repo; it also runs on its own from the Files app.
FROM nginx:1.30-alpine
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY round-timer.html /usr/share/nginx/html/index.html
COPY round-timer.html /usr/share/nginx/html/round-timer.html
COPY web/icon.svg /usr/share/nginx/html/icon.svg
# The source, easy to find inside the container.
COPY . /src
