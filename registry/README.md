# Gadget registry

The front door: a home page listing every gadget and service, a reverse proxy giving each
gadget its own port, a navbar injected into every gadget's pages, status dots, and HTTPS.

- Home page: http://<pc> and https://<pc>
- Gadgets: each on its own port (8101–8120), **http and https on the same port**:
  Photo Stitch `:8101`, Photo Drop `:8102` (http only), Video Tools `:8103`, Quote Finder `:8104`.

The registry knows nothing about gadgets by itself: it is **assembled** from every
`gadgets/<id>/gadget.json` when its image is built (see [docs/ARCHITECTURE.md](../docs/ARCHITECTURE.md)).
To add a gadget you never edit this folder; see [docs/ADDING-A-GADGET.md](../docs/ADDING-A-GADGET.md).

## What's here

| Path | Is |
|---|---|
| `site.json` | Site-wide settings: title, hosts (Portainer endpoint, build box), protocols, home ports, gadget port range, "I want to…" groups |
| `services.json` | Things it links to that aren't gadgets in this repo: Plex, Home Assistant, Portainer… (with icon specs) |
| `icons/` | The registry's own icons (its logo) |
| `html/` | Home page (Cards / Tiles), `_gadgets/nav.js` (navbar + `window.gadgets`), `_gadgets/ui.css` (shared look), `ca.html` (installing the certificate) |
| `nginx/40-gadgets.sh` | Writes the nginx config from `registry.json` when the container starts |
| `nginx/nginx.conf` | Stock nginx plus the per-port protocol switches (`stream.d`) |
| `Dockerfile` | Built from the repo root: stage 1 checks and assembles the manifests, stage 2 is nginx |

Deploy: `tools/trinket deploy registry` (builds on the build box, updates the Portainer stack).

## Views

The home page shows **Cards** (each gadget's `about` text: what it's for) or compact **Tiles**,
remembered per browser. A gadget with `pages` (Video Tools) shows one entry per page; the
navbar names whichever page you're on.

## Protocols: http, https or both

- **Both (default)**: every gadget port answers `http://` *and* `https://`. nginx's stream
  module peeks at the first bytes of each connection (`ssl_preread`): a TLS handshake goes to an
  HTTPS server, anything else to a plain-HTTP one (both on loopback, the client address passed
  along with the PROXY protocol). The home page uses 80 and 443.
- **Switch** the whole site with `"protocols"` in `site.json`, or without a rebuild with the
  `TRINKET_PROTOCOLS` environment variable on the registry stack (`https`, `http` or
  `http,https`). HTTPS only: plain requests are redirected (port 80 keeps `/ca.html` and
  `/ca.crt` so new devices can fetch the certificate). No certificate: everything is HTTP.
- **Per gadget**: `"proxy": {"protocols": ["http"]}` narrows it (Photo Drop: party guests'
  phones don't have the home certificate).
- Links on the home page and in the navbar keep the page's protocol when the target supports it.

## HTTPS certificate

From the home certificate authority ([infra/ca](../infra/ca)), in the external Docker volume
`gadget-tls` (`fullchain.crt`, `server.key`, `ca.crt`) mounted at `/etc/gadgets/tls`. New
devices: open http://<pc>/ca.html (in Safari on an iPhone). Machines that don't trust
the home CA or can't follow redirects use direct ports: Quote Finder's ingest and MCP on
`http://<pc>:8124`, video-tools' MCP on `:8090`.

## Gadget contract

What the registry relies on from a gadget:

- a container on the `gadgets` Docker network, reachable at `proxy.upstream`;
- HTML pages with a `</head>` (where the navbar script goes) and no compression of their own
  (the registry asks for plain responses);
- optionally `<meta name="gadget-nav" content="off|fab">`, and `var(--gadget-nav-h)` for
  full-height layouts under the bar.

Status dots probe each upstream's `/` with a HEAD request (502/504 = down).

`test/e2e.js` drives the home page and navbar in headless Chromium (on the build box).
