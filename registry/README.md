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
