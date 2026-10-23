#!/bin/sh
# Writes the nginx config from registry.json each time the container starts.
set -eu
REG=${REGISTRY_JSON:-/etc/gadgets/registry.json}
HTML=/usr/share/nginx/html
OUT=/etc/nginx/conf.d/gadgets.conf
cp "$REG" "$HTML/_gadgets/registry.json"

{
cat <<'HEAD'
map $http_upgrade $connection_upgrade { default upgrade; '' close; }
resolver 127.0.0.11 valid=30s ipv6=off;
gzip on;
gzip_types text/css application/javascript application/json image/svg+xml;

server {
  listen 80 default_server;
  root /usr/share/nginx/html;
  add_header X-Content-Type-Options nosniff always;
  location / { try_files $uri $uri/ =404; add_header Cache-Control no-cache; }
  location /_gadgets/ { add_header Cache-Control no-cache; add_header Access-Control-Allow-Origin *; }
HEAD
# status probes: /_gadgets/status/<id> answers 502/504 when the app is down
jq -r '.apps[] | select(.proxy or .url) | [.id, (.proxy.upstream // .url)] | @tsv' "$REG" | while IFS="$(printf '\t')" read -r id up; do
  case "$up" in */) ;; *) up="$up/";; esac
  cat <<EOP
  location = /_gadgets/status/$id {
    set \$probe "$up";
    proxy_pass \$probe;
    proxy_method HEAD;
    proxy_ssl_verify off;
    proxy_ssl_server_name on;
    proxy_connect_timeout 3s;
    proxy_read_timeout 5s;
    proxy_pass_request_body off;
    proxy_set_header Content-Length "";
    add_header Cache-Control no-store;
  }
EOP
done
echo "}"

# one server per proxied gadget, on its own port, with the navbar added
jq -r '.apps[] | select(.proxy) | [.id, .proxy.port, .proxy.upstream, (.proxy.nav // "bar")] | @tsv' "$REG" | while IFS="$(printf '\t')" read -r id port up nav; do
  cat <<EOS

# $id
server {
  listen $port;
  location /_gadgets/ { root /usr/share/nginx/html; add_header Cache-Control no-cache; }
  location / {
    set \$up "$up";
    proxy_pass \$up;
    include /etc/nginx/gadget-proxy.conf;
EOS
  if [ "$nav" != "off" ]; then
    cat <<'EOI'
    sub_filter_types text/html;
    sub_filter_once on;
    sub_filter '</head>' '<script src="/_gadgets/nav.js" defer></script></head>';
EOI
  fi
  cat <<'EOE'
  }
  error_page 502 504 /_gadgets/offline.html;
}
EOE
done
} > "$OUT"
echo "gadgets: wrote $OUT ($(jq '[.apps[]|select(.proxy)]|length' "$REG") proxied, $(jq '.apps|length' "$REG") listed)"
