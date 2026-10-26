# Adding a gadget

A gadget is one folder under `gadgets/`. Nothing else needs editing: the registry picks it up
from its `gadget.json` the next time the registry is deployed.

## 1. Start it

    tools/trinket new egg-timer --name "Egg Timer" --template go      # or --template static

- **go**: a Go server (standard library) with an embedded page, a JSON API, `/healthz` and a
  unit test. For anything that keeps data, talks to other services or does work on the server.
- **static**: HTML, CSS and JavaScript served by nginx. For things that run entirely in the
  browser (Photo Stitch is one).

This creates `gadgets/egg-timer/` on the next free port (8105, say), with an icon drawn in its
own colours. `tools/trinket list` shows what's taken.

## 2. Describe it (`gadget.json`)

| Field | What to put |
|---|---|
| `desc` | One line, shown on tiles and in the menu. |
| `about` | Two or three sentences for the Cards view: what it's for, how you'd use it. |
| `iconSpec.glyph` | White line art on a 64×64 grid. `{S}` = the standard white stroke, `{F}` = white fill. `tools/trinket icons` redraws it. Or drop in your own `web/icon.svg` and remove `iconSpec`. |
| `intents` | "I want to…" phrases, each starting with "…" (e.g. "…time my eggs perfectly"), with a sentence and a `group` from `registry/site.json`. |
| `proxy.protocols` | Leave it out to follow the site (http and https). `["http"]` only if visitors' devices won't have the home certificate (Photo Drop's party guests). |
| `proxy.nav` | `bar` (default), `fab` (a small floating button, for full-screen pages) or `off`. |
| `pages` | If one app has several tools that deserve their own tiles (Video Tools). |
| `mcp` | If it has an MCP endpoint for agents: its URL and tool names. |
| `uses` | Other gadgets it talks to. |
| `deploy.stacks` | Usually as generated. Add `secrets: ["NAME"]` for environment values that must not be in git. |

`tools/trinket check` validates it; the registry image runs the same check and refuses to build
with a broken manifest.

## 3. Build it

Develop on the build box (`ssh buildbox`), never on the Pi. A few conventions the existing
gadgets follow:

- Talk to other gadgets **server-to-server** over the `gadgets` Docker network
  (`http://video-tools:8080`); in the browser, link with
  `await gadgets.url('video-tools', '/jobs/j_1')` (from the navbar script) rather than
  hard-coding ports or protocols.
- Use `/_gadgets/ui.css` (tokens like `var(--accent)`, `.card`, `.btn`, `.field`, `.seg`) and
  you get light/dark mode and the shared look for free.
- Test at **phone width** (390 px) as well as desktop: `node test/e2e.js https://<pc>:8105`
  on the build box (it has Chromium and puppeteer).
- **No addresses, host names or local paths in the repo** (it's public). In `gadget.json` write
  `{pc}` / `{pi}`; in a compose file `${PC_ADDR:?set PC_ADDR}` and, for a folder on a host, a
  name of your own (`${PHOTOS_DIR:?set PHOTOS_DIR}`) with its value under `env` in
  `registry/site.local.json`. `tools/trinket deploy` fills them in.
- Keep containers small (`mem_limit`), and the Pi's work light: anything heavy runs on the PC.
- Secrets: `~/.config/shiny-trinkets/<stack>.env` on the machine you deploy from, never git.

## 4. Deploy it

    tools/trinket deploy egg-timer     # builds on the build box, creates/updates the Portainer stack
    tools/trinket deploy registry      # so the home page, menu and proxy know about it

After the first time, only `deploy egg-timer` is needed for code changes; redeploy the registry
when `gadget.json` changes (name, icon, port, pages, intents).

## 5. Tell people

Add it to the list in the top-level README and write its own README (what it does, how to use
it, anything surprising). That's it.

## Removing a gadget

Delete its folder, `tools/trinket deploy registry`, and remove its stack in Portainer.
