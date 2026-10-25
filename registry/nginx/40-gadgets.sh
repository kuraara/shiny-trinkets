#!/bin/sh
# Writes the nginx config from registry.json each time the container starts.
#
# HTTPS: when a certificate is mounted in $TLS_DIR (fullchain.crt + server.key, and ca.crt to
# hand out to phones), everything is served over HTTPS: the home page on 443 (port 80 redirects,
# apart from the certificate download page) and every gadget on its own port, where a plain
# http:// request is redirected to https:// on the same port. A gadget with "proxy": {"tls": false}
# stays plain HTTP (Photo Drop: party guests' phones don't have the home certificate).
# Without a certificate everything is plain HTTP, as before.
set -eu
REG=${REGISTRY_JSON:-/etc/gadgets/registry.json}
TLS_DIR=${TLS_DIR:-/etc/gadgets/tls}
HTML=/usr/share/nginx/html
OUT=/etc/nginx/conf.d/gadgets.conf
cp "$REG" "$HTML/_gadgets/registry.json"

TLS=0
if [ -s "$TLS_DIR/fullchain.crt" ] && [ -s "$TLS_DIR/server.key" ]; then TLS=1; fi
rm -f "$HTML/ca.crt"
if [ -s "$TLS_DIR/ca.crt" ]; then cp "$TLS_DIR/ca.crt" "$HTML/ca.crt"; fi

ssl_lines() {
  cat <<EOS
  ssl_certificate $TLS_DIR/fullchain.crt;
  ssl_certificate_key $TLS_DIR/server.key;
  ssl_protocols TLSv1.2 TLSv1.3;
  ssl_session_cache shared:SSL:2m;
  # plain http:// to this port: send it to https:// on the same port
  error_page 497 =301 https://\$host:\$server_port\$request_uri;
EOS
}

{
cat <<'HEAD'
map $http_upgrade $connection_upgrade { default upgrade; '' close; }
resolver 127.0.0.11 valid=30s ipv6=off;
gzip on;
gzip_types text/css application/javascript application/json image/svg+xml;
HEAD

if [ "$TLS" = 1 ]; then
  # port 80: only the certificate and how to install it; everything else moves to https://
  cat <<'P80'

server {
  listen 80 default_server;
  root /usr/share/nginx/html;
  location = /ca.crt { types { } default_type application/x-x509-ca-cert; add_header Cache-Control no-cache; }
  location = /ca.html { add_header Cache-Control no-cache; }
  location / { return 301 https://$host$request_uri; }
}
P80
  echo
  echo "server {"
  echo "  listen 443 ssl default_server;"
  ssl_lines
else
  echo
  echo "server {"
  echo "  listen 80 default_server;"
fi
cat <<'HOME'
  root /usr/share/nginx/html;
  add_header X-Content-Type-Options nosniff always;
  location / { try_files $uri $uri/ =404; add_header Cache-Control no-cache; }
  location = /ca.crt { types { } default_type application/x-x509-ca-cert; add_header Cache-Control no-cache; }
  location /_gadgets/ { add_header Cache-Control no-cache; add_header Access-Control-Allow-Origin *; }
HOME
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
# (several entries may share a port, each opening its own path: one server per port)
jq -r '[.apps[] | select(.proxy)] | group_by(.proxy.port) | map(.[0])[]
       | [.id, .proxy.port, .proxy.upstream, (.proxy.nav // "bar"), (if .proxy.tls == false then "off" else "on" end)] | @tsv' "$REG" |
while IFS="$(printf '\t')" read -r id port up nav tls; do
  echo
  echo "# $id"
  echo "server {"
  if [ "$TLS" = 1 ] && [ "$tls" = on ]; then
    echo "  listen $port ssl;"
    ssl_lines
  else
    echo "  listen $port;"
  fi
  cat <<EOS
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
echo "gadgets: wrote $OUT ($(jq '[.apps[]|select(.proxy)|.proxy.port]|unique|length' "$REG") proxied ports, $(jq '.apps|length' "$REG") listed, https $( [ "$TLS" = 1 ] && echo on || echo off))"
