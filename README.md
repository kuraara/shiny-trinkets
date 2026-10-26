# shiny-trinkets

Small self-hosted web apps for a home network ("gadgets"), each one a self-contained folder, and
a **registry** that puts them behind one front door: a home page, a shared navbar, HTTPS, and
status dots. They run as Docker containers managed by Portainer on a PC (Docker Desktop) and a
Raspberry Pi.

| Gadget | What it does |
|---|---|
| [Quote Finder](gadgets/quote-finder) | Search the dialogue of everything on Plex: find the episode from a line you remember, then turn the scene into comic-style panels or a clip (with an MCP endpoint for agents). |
| [Video Tools](gadgets/video-tools) | Panel transcripts (stills with the dialogue as subtitles), joining clips with fades, cutting clips; speech recognition and speaker hints. Web UI, JSON API and MCP. |
| [Photo Drop](gadgets/photo-drop) | A live party photo wall: guests scan a QR code and send photos, you approve them, they land on the TV like prints thrown on a desk. |
| [Photo Stitch](gadgets/photo-stitch) | Put photos together in a column, row or grid with borders, gaps and an exact size, entirely in the browser. |
| [AirPrint to PDF](gadgets/air-pdf-printer) | A printer called "PDF" for iPhones and Macs; whatever you print is saved as a PDF. |

Plus the [registry](registry) (home page, proxy, navbar) and the [build box](infra/buildbox)
(where everything is compiled and tested).

## How it fits together

- **A gadget is a folder** under `gadgets/` with a `gadget.json` manifest: name, descriptions,
  icon, port, pages, "I want to…" phrases, MCP endpoint, and how to deploy it.
- **The registry is assembled** from those manifests when its image is built, so adding a
  gadget never means editing the registry.
- **http and https on the same port**: every gadget answers both (switchable site-wide or per
  gadget), with certificates from a small, name-constrained home certificate authority.
- **One tool**, `tools/trinket`: `list`, `check`, `new`, `icons`, `deploy`.

Details: [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) (including the review that led here) ·
[docs/ADDING-A-GADGET.md](docs/ADDING-A-GADGET.md) · [docs/ROADMAP.md](docs/ROADMAP.md).

## Quick start

    tools/trinket list                                 # what's here
    tools/trinket new egg-timer --template go          # a new gadget on the next free port
    tools/trinket check                                # validate every manifest
    tools/trinket deploy egg-timer                     # build on the build box, update Portainer
    tools/trinket deploy registry                      # so the home page and menu know about it

`tools/trinket` needs only Python 3. Deploying needs a Portainer API key (`~/portainer-mcp.env`)
and SSH to the build box; secrets for new stacks go in `~/.config/shiny-trinkets/<stack>.env`.
Nothing secret is ever committed.

## The network this was built for

A Windows PC (Docker Desktop, Portainer agent) that does the heavy lifting and all builds, and a
Raspberry Pi (Portainer, Plex, the media). Their addresses and the paths on them are **not in this
repo**: they go in `registry/site.local.json` (git-ignored; copy
[`registry/site.local.example.json`](registry/site.local.example.json)). Manifests and
`services.json` write a host's address as `{pc}` / `{pi}`, compose files as `${PC_ADDR}` /
`${PI_ADDR}`, and `tools/trinket` fills them in when it builds and deploys. `tools/trinket check`
refuses a local address written into a manifest or compose file.
