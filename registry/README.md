# Gadget registry

One nginx container on the PC that lists everything running on the PC and the Pi, and
reverse-proxies the home-made gadgets so they all get the same top bar.

- **Home page**: `http://<pc>/`: cards for every gadget and service with a live up/down dot.
- **Gadgets** each get their own port on the PC (no path rewriting, so apps need no changes):
  Photo Stitch `:8101`, Photo Drop `:8102`, Video Tools `:8103`. Ports up to `:8110` are mapped already.
- **Navbar**: nginx adds `<script src="/_gadgets/nav.js">` to every proxied HTML page. It draws a
  dark top bar with a hamburger menu of every app (in a shadow root, so page CSS can't touch it).
  A page can opt out with `<meta name="gadget-nav" content="off">` (Photo Drop's guest page and
  desk display do), or ask for a small floating button with `content="fab"`. Pages that need full
  height can use `var(--gadget-nav-h)`.
- **Services** (Home Assistant, Sonarr, Portainer, ...) are listed and linked directly, not proxied:
  their UIs don't take kindly to an injected bar, and Home Assistant refuses proxies unless its
  config trusts them.

## Adding an app

1. Add it to `registry.json` (`kind`: gadget / service / background; gadgets get
   `"proxy": {"port": 81xx, "upstream": "http://container:port"}` and must join the `gadgets` network).
2. Add an icon to `tools/make-icons.py` and run it (or drop an SVG in `html/_gadgets/icons/<id>.svg`).
3. Rebuild and redeploy (`buildbox/redeploy.sh gadget-registry <stackId>`).

`nginx/40-gadgets.sh` turns `registry.json` into the nginx config each time the container starts.

## Views and shared ports

The home page shows **Cards** (with each gadget's `about` text: what it's for) or compact
**Tiles**; the choice is remembered per browser. Several entries can share one proxied port,
each opening its own `proxy.path` (Video Tools is split into Panel Transcript, Join Clips and
Cut a Clip this way; the `hidden` video-tools entry owns the port). nginx gets one server per
port, and the navbar names whichever entry's path you're on.

## HTTPS (home certificate)

Everything goes over HTTPS with a certificate from a small home certificate authority, so phones
treat the gadgets as secure (needed for the share sheet, e.g. "Save Video" to Photos, and for
clipboard access).

- **The authority** lives on the build box in `/work/ca` (`ca.key` never leaves it; not in git).
  It is name-constrained: it can only vouch for the home network and `.local`, `.lan`,
  `.home.arpa` names, never a real website.
- **Server certificate** (`server.crt`, for <pc>, <pi> and <pi-name>.local) is in
  the external Docker volume `gadget-tls` (`fullchain.crt`, `server.key`, `ca.crt`), mounted at
  `/etc/gadgets/tls`. Without it, the registry falls back to plain HTTP.
- **Ports**: home page on 443 (port 80 redirects, apart from `/ca.html` and `/ca.crt`, which stay
  plain HTTP so a new device can fetch the certificate); every proxied gadget keeps its port and
  answers HTTPS, redirecting `http://` to `https://` on the same port. Entries with
  `"proxy": {"tls": false}` stay plain HTTP: Photo Drop, because party guests' phones don't have
  the certificate.
- **New devices**: open http://<pc>/ca.html (in Safari on an iPhone) and follow it.
- **Portainer** on the Pi (9443) uses a certificate from the same authority (`/work/ca/portainer.*`),
  uploaded through Portainer's `PUT /api/ssl`.
- **Machines** that can't follow a redirect to https use direct ports instead: Quote Finder's
  ingest and MCP on `http://<pc>:8124`, video-tools' MCP on `:8090`.

**Renewing** (certificates expire 2 January 2029; the authority in 2036), on the build box:

    cd /work/ca
    openssl req -new -key server.key -out server.csr -config server.cnf
    openssl x509 -req -in server.csr -CA ca.crt -CAkey ca.key -CAserial ca.srl -out server.crt \
      -days 800 -sha256 -extfile server.cnf -extensions v3_srv
    cat server.crt ca.crt > fullchain.crt
    docker create --name t -v gadget-tls:/t alpine:3.22 true && docker cp fullchain.crt t:/t/ && docker rm t
    docker restart gadget-registry

(and the same with `portainer.*`, then upload with `PUT /api/ssl`). Devices don't need anything new
until the authority itself expires.
