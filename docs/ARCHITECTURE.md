# Architecture

shiny-trinkets is a set of small self-hosted web apps ("gadgets") for a home network, plus a
**registry** that puts them all behind one front door: a home page, a reverse proxy, a shared
navbar, HTTPS, and status dots. Everything runs as Docker containers managed by Portainer on two
machines: a Windows PC running Docker Desktop (<pc>, does the heavy lifting and all
builds) and a Raspberry Pi (<pi>, media, Plex, Portainer itself).

This document is the review of how it was put together, and the module design it moved to.

## Review: what made adding a gadget hard

The first five gadgets were built quickly, one after another, in a shared "misc projects" repo.
Each one works, but the seams between them grew by hand:

| Problem | What it looked like |
|---|---|
| **One central list** | Every gadget's name, description, port, upstream and kind lived in `gadget-registry/registry.json`. Adding a gadget meant editing the registry, not the gadget. |
| **Icons in the wrong place** | All icons were drawn by one script in the registry, from a table inside the script. |
| **Hand-made multi-page apps** | Video Tools' three tools were three registry entries plus a hidden fourth one, copied by hand, sharing a port. |
| **Hard-coded links between gadgets** | Quote Finder knew video-tools' public address (`VIDEO_TOOLS_PUBLIC_URL`), which broke the moment protocols or ports changed. |
| **Deploys by memory** | A `redeploy.sh` outside the repo with Portainer stack numbers passed by hand; Video Tools needed a different command because its compose file's `build:` block spans lines; Pi images needed a separate cross-compile + upload dance. |
| **No checks** | Nothing caught two gadgets on one port, a missing icon, or a typo in a host name until the registry was deployed. |
| **Fixed port window** | The registry published exactly 8101–8110. |
| **Protocols as an afterthought** | HTTPS was bolted on with a per-port redirect, which broke machine clients (the subtitle extractor's uploads, MCP) and made Photo Drop a special case in three places. |
| **Every gadget re-invents its look** | Colours, buttons, dark mode and the `[hidden]` fix were copied between gadgets. |
| **Agents' entry points undocumented** | MCP endpoints existed on two gadgets but nothing listed them. |

None of this is wrong for five gadgets. It just means gadget six would touch five places.

## Design: a gadget is a folder

```
gadgets/<id>/
  gadget.json          the manifest: everything the rest of the system needs to know
  icon.svg             (or drawn from the manifest's iconSpec)
  docker-compose.yml   how it runs (one or more stacks)
  Dockerfile, sources, README.md, test/
```

Nothing outside the folder needs editing to add, change or remove a gadget. The registry is
**assembled** from the manifests when its image is built.

### The manifest (`gadget.json`)

Schema: [`gadget.schema.json`](gadget.schema.json). The important parts:

```jsonc
{
  "id": "quote-finder",             // = folder name; lowercase-dashes
  "name": "Quote Finder",
  "kind": "gadget",                 // gadget | service | background
  "host": "pc",                     // a host from registry/site.json
  "desc": "One line.",              // Tiles / navbar
  "about": "A few sentences.",      // Cards view
  "icon": "web/icon.svg",           // file in the folder…
  "iconSpec": { "top": "#14b8a6", "bottom": "#0f766e", "glyph": "<path … {S}/>" },  // …or drawn
  "proxy": {                        // served through the registry
    "port": 8104,                   // its public port (registry/site.json portRange)
    "upstream": "http://quote-finder:8080",   // as the registry reaches it (gadgets network)
    "protocols": ["http", "https"], // optional: narrow the site's protocols (Photo Drop: ["http"])
    "nav": "bar"                    // bar | fab | off: the injected navbar
  },
  "url": "http://…",                // instead of proxy: a page served elsewhere (e.g. on the Pi)
  "pages": [ { "id": "cut-clip", "name": "Cut a Clip", "path": "/clip", … } ],  // one app, several tools
  "intents": [ { "phrase": "…know which show or movie a quote came from", "group": "video" } ],
  "mcp": { "url": "http://<pc>:8124/mcp", "tools": "…" },
  "uses": ["video-tools"],          // other gadgets it talks to (checked to exist)
  "deploy": { "stacks": [ { "name": "quote-finder", "host": "pc", "compose": "docker-compose.yml",
                             "build": { "image": "quote-finder:latest", "context": "." },
                             "secrets": ["INGEST_TOKEN"] } ] }
}
```

- **pages** replace the hidden-entry trick: Video Tools is one app on one port whose three pages
  appear as three gadgets in the registry and navbar.
- **intents** are the "I want to…" phrases (the landing view in [ROADMAP.md](ROADMAP.md)); each
  points at the gadget or one of its pages.
- **deploy.stacks** can name several stacks on different hosts: Quote Finder's server runs on the
  PC and its subtitle extractor on the Pi (next to the media), cross-compiled on the build box.

### The registry

`registry/` holds only what is about the *whole* site:

- `site.json`: title, hosts (with their Portainer endpoint and whether the build box shares
  their Docker), protocols, home ports, the gadget port range, intent groups.
- `services.json`: things it links to that aren't gadgets here (Plex, Home Assistant…).
- `html/`: the home page (Cards / Tiles), the navbar (`_gadgets/nav.js`), the shared look
  (`_gadgets/ui.css`), the certificate page (`ca.html`).
- `nginx/`: the config generator (`40-gadgets.sh`), run when the container starts.

Its image is built from the repo root: a first stage runs `tools/trinket check` and
`tools/trinket assemble`, turning manifests + site + services into `registry.json` and an icon
set; the second stage is nginx. A broken manifest fails the build, not the live site.

### How a request flows

```
browser ──► registry :8104 ─┬─ TLS handshake? ──► https server ─┐
                            └─ plain HTTP ──────► http server  ─┴─► quote-finder:8080
                                                   (+ navbar injected into HTML)
```

- **One port, both protocols.** nginx's stream module peeks at the first bytes on each gadget
  port (`ssl_preread`): a TLS handshake goes to an HTTPS server, anything else to a plain one,
  both on loopback, with the client address passed along (PROXY protocol). So
  `http://pc:8104` and `https://pc:8104` both work; no second port range, no redirects that
  machine clients trip over.
- **Switchable.** `site.json` `protocols` (or `TRINKET_PROTOCOLS` on the registry container,
  no rebuild) picks `http`, `https` or both; a gadget can narrow it. With HTTPS only, plain
  requests get a redirect; with no certificate, everything is HTTP.
- **Certificates** come from a small home certificate authority ([infra/ca](../infra/ca)),
  name-constrained to the home network so it can't vouch for real websites.
- **Navbar**: nginx adds `<script src="/_gadgets/nav.js">` before `</head>`; the script draws
  the bar in a shadow DOM (no style clashes). Pages opt out with
  `<meta name="gadget-nav" content="off">` or ask for a floating button with `content="fab"`.
- **Links between gadgets**: every page gets `window.gadgets.url(id, path)`, which knows each
  gadget's port, path and protocols from the registry, e.g.
  `await gadgets.url('video-tools', '/jobs/j_1')`. Servers talk to each other directly over the
  `gadgets` Docker network (`http://video-tools:8080`), never through the public ports.
- **Machines** that can't follow redirects or don't trust the home CA use direct ports
  (Quote Finder's ingest/MCP on 8124, video-tools' MCP on 8090).

### Building and deploying

`tools/trinket` (Python, standard library only) is the one tool:

| Command | Does |
|---|---|
| `trinket list` | gadgets, ports, protocols, stacks; next free port |
| `trinket check` | validates every manifest: required fields, ids = folders, unique ids and ports, ports in range, icons present, intents/pages/uses consistent, hosts known, compose files exist |
| `trinket new <id> --template go\|static` | copies a template, picks the next free port, draws an icon |
| `trinket icons` | redraws icons from `iconSpec`s |
| `trinket assemble --out DIR` | what the registry image runs |
| `trinket deploy <id>\|registry` | for each stack: build (on the build box; Go binaries cross-compiled there for the Pi, whose image is then built through Portainer), strip `build:` from the compose file, then update the Portainer stack found **by name** (keeping its environment, so secrets stay put) or create it (secrets from `~/.config/shiny-trinkets/<stack>.env`) |

Rules that come from experience: the Pi never compiles or hosts development work (it ran out of
memory once); builds happen on the build box ([infra/buildbox](../infra/buildbox)), which shares
the PC's Docker; secrets never go in git.

### Templates

`templates/go` (a Go standard-library server with an embedded page, a JSON API, `/healthz`, a
unit test and a two-stage Dockerfile) and `templates/static` (HTML/JS served by nginx). Both
link `/_gadgets/ui.css`, include a phone + desktop browser test, and come with a manifest that
passes `trinket check` as soon as they're created.

## What stays specific to a gadget

The registry doesn't know or care what a gadget does. A gadget may have its own API, MCP
endpoint, background workers (the subtitle extractor), extra stacks on other hosts or its own
data volume. The only contract is: **a manifest, a container reachable from the registry, and
(optionally) `/healthz`**.
