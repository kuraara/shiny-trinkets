#!/bin/sh
# Writes the nginx config from registry.json (assembled from every gadget's gadget.json) each
# time the container starts.
#
# Protocols: http, https or both. Site-wide from registry.json "protocols" (from site.json),
# overridable without a rebuild with the TRINKET_PROTOCOLS env var ("http,https", "https",
# "http"); a gadget can narrow it with "proxy": {"protocols": [...]} (Photo Drop: http only, for
# party guests whose phones don't have the home certificate). HTTPS needs a certificate in
# $TLS_DIR (fullchain.crt + server.key; ca.crt is handed out at /ca.crt); without one, all is HTTP.
#
# "Both" means both on the same port: a tiny stream proxy on each gadget port peeks at the first
# bytes (ssl_preread) and sends a TLS handshake to the HTTPS server and anything else to the
# plain-HTTP one, so http://pc:8104 and https://pc:8104 both work. The home page uses 80 and 443.
# With https only, plain http:// is redirected; with http only, a port is plain HTTP.
set -eu
REG=${REGISTRY_JSON:-/etc/gadgets/registry.json}
TLS_DIR=${TLS_DIR:-/etc/gadgets/tls}
HTML=/usr/share/nginx/html
CONF=/etc/nginx/conf.d/gadgets.conf
STREAM=/etc/nginx/stream.d/gadgets.conf
INC=/etc/nginx/gadgets.d
mkdir -p "$(dirname "$STREAM")" "$INC"
rm -f "$INC"/*.inc
cp "$REG" "$HTML/_gadgets/registry.json"
rm -f "$HTML/ca.crt"
[ -s "$TLS_DIR/ca.crt" ] && cp "$TLS_DIR/ca.crt" "$HTML/ca.crt"

has() { case ",$1," in *",$2,"*) return 0;; esac; return 1; }
WANT=${TRINKET_PROTOCOLS:-$(jq -r '(.protocols // ["http","https"]) | join(",")' "$REG")}
TLS=0
if [ -s "$TLS_DIR/fullchain.crt" ] && [ -s "$TLS_DIR/server.key" ]; then TLS=1; fi
SITE=""
has "$WANT" http && SITE="http"
if has "$WANT" https; then
  if [ "$TLS" = 1 ]; then SITE="${SITE:+$SITE,}https"; else echo "gadgets: https wanted but no certificate in $TLS_DIR: plain HTTP only"; SITE="http"; fi
fi
[ -n "$SITE" ] || SITE="http"

ssl_lines() {
  cat <<EOS
  ssl_certificate $TLS_DIR/fullchain.crt;
  ssl_certificate_key $TLS_DIR/server.key;
  ssl_protocols TLSv1.2 TLSv1.3;
  ssl_session_cache shared:SSL:2m;
EOS
}
# behind the per-port switch: the real client address arrives by the PROXY protocol, and
# redirects must not mention the internal port
behind_switch() {
  cat <<'EOS'
  set_real_ip_from 127.0.0.1;
  real_ip_header proxy_protocol;
  absolute_redirect off;
EOS
}

# ---- the home page (and status probes), shared by its http and https servers
{
  cat <<'HOME'
  root /usr/share/nginx/html;
  add_header X-Content-Type-Options nosniff always;
  location / { try_files $uri $uri/ =404; add_header Cache-Control no-cache; }
  location = /ca.crt { types { } default_type application/x-x509-ca-cert; add_header Cache-Control no-cache; }
  location /_gadgets/ { add_header Cache-Control no-cache; add_header Access-Control-Allow-Origin *; }
HOME
  # status probes: /_gadgets/status/<id> answers 502/504 when the app is down
  jq -r '.apps[] | select(.proxy or .url) | [.id, (.proxy.upstream // .url)] | @tsv' "$REG" |
  while IFS="$(printf '\t')" read -r id up; do
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
} > "$INC/home.inc"

{
cat <<'HEAD'
map $http_upgrade $connection_upgrade { default upgrade; '' close; }
resolver 127.0.0.11 valid=30s ipv6=off;
gzip on;
gzip_types text/css application/javascript application/json image/svg+xml;
HEAD

HTTP_HOME=$(jq -r '.home.http // 80' "$REG"); HTTPS_HOME=$(jq -r '.home.https // 443' "$REG")
if has "$SITE" http; then
  printf '\nserver {\n  listen %s default_server;\n  include %s/home.inc;\n}\n' "$HTTP_HOME" "$INC"
else
  # https only: port 80 keeps just the certificate and how to install it; the rest moves over
  cat <<P80

server {
  listen $HTTP_HOME default_server;
  root /usr/share/nginx/html;
  location = /ca.crt { types { } default_type application/x-x509-ca-cert; add_header Cache-Control no-cache; }
  location = /ca.html { add_header Cache-Control no-cache; }
  location / { return 301 https://\$host\$request_uri; }
}
P80
fi
if has "$SITE" https; then
  printf '\nserver {\n  listen %s ssl default_server;\n' "$HTTPS_HOME"; ssl_lines; printf '  include %s/home.inc;\n}\n' "$INC"
fi

# ---- one gadget server per port (several entries may share a port: one app, several pages)
echo "stream {" > "$STREAM.tmp"
MUXED=0
jq -r '[.apps[] | select(.proxy)] | group_by(.proxy.port) | map(.[0])[]
       | [.id, .proxy.port, .proxy.upstream, (.proxy.nav // "bar"), ((.proxy.protocols // ["http","https"]) | join(","))] | @tsv' "$REG" |
{
while IFS="$(printf '\t')" read -r id port up nav prot; do
  # what this port speaks: the site's protocols, narrowed by the gadget's
  P=""
  has "$SITE" http && has "$prot" http && P="http"
  has "$SITE" https && has "$prot" https && P="${P:+$P,}https"
  [ -n "$P" ] || P=$(has "$SITE" https && echo https || echo http)   # gadget asks for something the site doesn't do
  plain=$((port + 20000)); secure=$((port + 30000))

  { # the gadget itself
    cat <<EOS
  location /_gadgets/ { root /usr/share/nginx/html; add_header Cache-Control no-cache; }
  location / {
    set \$up "$up";
    proxy_pass \$up;
    include /etc/nginx/gadget-proxy.conf;
EOS
    if [ "$nav" != "off" ]; then
      cat <<'EOI'
    sub_filter_once on;
    sub_filter '</head>' '<script src="/_gadgets/nav.js" defer></script></head>';
EOI
    fi
    printf '  }\n  error_page 502 504 /_gadgets/offline.html;\n'
  } > "$INC/$port.inc"

  printf '\n# %s (%s)\n' "$id" "$P"
  case "$P" in
    http)
      printf 'server {\n  listen %s;\n  include %s/%s.inc;\n}\n' "$port" "$INC" "$port" ;;
    *)
      # the switch on the public port, then an https server and either a plain one or a redirect
      cat >> "$STREAM.tmp" <<EOM
  map \$ssl_preread_protocol \$to_$port { "" 127.0.0.1:$plain; default 127.0.0.1:$secure; }
  server { listen $port; ssl_preread on; proxy_protocol on; proxy_pass \$to_$port; }
EOM
      printf 'server {\n  listen 127.0.0.1:%s ssl proxy_protocol;\n' "$secure"; ssl_lines; behind_switch
      printf '  include %s/%s.inc;\n}\n' "$INC" "$port"
      printf 'server {\n  listen 127.0.0.1:%s proxy_protocol;\n' "$plain"; behind_switch
      if [ "$P" = "https" ]; then
        printf '  location / { return 301 https://$host:%s$request_uri; }\n}\n' "$port"
      else
        printf '  include %s/%s.inc;\n}\n' "$INC" "$port"
      fi ;;
  esac
done
}
} > "$CONF"
echo "}" >> "$STREAM.tmp"
if grep -q "listen" "$STREAM.tmp"; then mv "$STREAM.tmp" "$STREAM"; else rm -f "$STREAM.tmp"; : > "$STREAM"; fi
echo "gadgets: wrote $CONF ($(jq '[.apps[]|select(.proxy)|.proxy.port]|unique|length' "$REG") gadget ports, $(jq '.apps|length' "$REG") entries; protocols: $SITE)"
