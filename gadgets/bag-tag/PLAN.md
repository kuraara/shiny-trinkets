# Bag Tag: build plan (hand-off to Claude Code)

Written 2026-11-08. Status key: [ ] todo · [~] in progress · [x] done.

**Goal:** a gadget where you type a name, pick a font and get the two files for a two-colour,
press-fit bag tag (yellow base + white name/flower inlay) to print on the Prusa MK3S+. Phone
first: most use is from an iPhone.

The geometry is **done and tested**. It lives in `generator/` (vendored from
[designing-stuff/bag-tag](https://github.com/kuraara/designing-stuff/tree/main/bag-tag); read
that README first for the design, the parameters and how the two parts fit). This gadget is a
web UI + API + MCP **around `generator/bag_tag.py`**, which is treated as a black box with a JSON
interface. Do not re-implement the geometry in Go or JS.

The scaffold in this folder (`main.go`, `web/`, `Dockerfile`, `test/e2e.js`) is the stock
`templates/go` output; replace it as below. `gadget.json` is final (port 8106, intent group
`making`, MCP URL, icon), apart from adjusting the MCP tool list if it changes.

## Rules (from CLAUDE.md, plus the owner's preferences)

- Build and test on the build box (`ssh -F ~/.ssh/config buildbox`), never on the Pi. Commit
  straight to `master`, one commit per phase. Deploy with `tools/trinket deploy bag-tag`, then
  `tools/trinket deploy registry` (the manifest is new).
- Public repo: no addresses, host names or local paths. Write `{pc}` in manifests and `<pc>` in docs.
- Test pages in headless Chromium at **390 px and 1280 px**, light and dark, before calling a
  phase done.
- Owner's preferences:
  - **Go** for the server (stdlib, like the template).
  - **The source code goes inside the container and is easy to find** (`/src`, plus a "Source"
    link in the page footer).
  - **An MCP endpoint** so agents can make tags.
  - Keep the image lean where it's cheap to.

## What the generator gives you (verified 2026-11-08)

```
python3 generator/bag_tag.py --name AUBREY --font "Sniglet:style=ExtraBold" \
    --letter-h 12 --max-len 62 --dir down --set clearance=0.1 --out DIR --stem aubrey --json
```

- **Output files:** `DIR/aubrey_base.stl` and `aubrey_inlay.stl` (binary, already in print
  orientation), `aubrey_preview.svg` (top view with an AAA battery for scale, dimensions
  underneath) and `aubrey_info.json`.
- **stdout (`--json`):** the info JSON, e.g.

  ```json
  {"name": "AUBREY", "font": "...", "letter_h": 10.81, "length": 99.7, "width": 28.8,
   "thickness": 6, "inlay_h": 4.6, "inlay_face_down": true, "inlay_pieces": 2,
   "warnings": ["long name: letters shrunk to 10.8 mm ..."],
   "files": {...}, "engine": "manifold", "seconds": 3.7}
  ```
- **On bad input:** exit code 2, stdout `{"error": "Sniglet has no letters for: 🦄"}`.
  The checks cover:
  - unknown font or style
  - characters missing from the font
  - a name over 24 characters
  - an unknown `--set` key
- **`--preview-only`:** SVG + info, no STLs. **About 0.6 s**; use it for the live preview.
- **Full build:** **1–4 s**, peak RSS about **200 MB**.
- **`--list-fonts`:** JSON list `[{font, family, style, bundled, file}]`, with the 7 bundled
  fonts first, then everything fontconfig sees.
- **`--params`:** every model parameter as `{name: {default, group, help, choices?}}`, grouped
  `Text`, `Body`, `Flower`, `Keyring loop`, `Inlay`. Build the "Advanced" panel from this, so
  the UI follows the model without code changes.
  - `name`, `font`, `text_dir` and `text_size` are set with their own flags (`--name`, `--font`,
    `--dir`, `--text-size`), not `--set`.
- **Dependencies:**
  - python3 with numpy, manifold3d, shapely and fontTools
  - OpenSCAD 2021.01+, used for 2D only, so it runs headless with no X or xvfb
  - fontconfig (`fc-list`, `fc-scan`)
  - Without manifold3d it still works, but takes minutes. Make sure the image has it
    (`"engine": "manifold"` in the info).

## Architecture

```
browser ──► registry :8106 ──► bag-tag:8080 (Go)
                                ├─ GET  /                page (embedded web/)
                                ├─ GET  /fonts/*.ttf     bundled fonts, for @font-face samples
                                ├─ GET  /api/fonts       cached --list-fonts (no file paths)
                                ├─ GET  /api/params      cached --params
                                ├─ POST /api/preview     --preview-only  → {info, svg}
                                ├─ POST /api/make        full build      → {id, info, files{url…}}
                                ├─ GET  /files/{id}/{f}  STL / SVG / JSON / zip downloads
                                ├─ POST /mcp             MCP (JSON-RPC over HTTP)
                                └─ GET  /healthz
                                   exec: python3 /src/generator/bag_tag.py … --json
                                   cache: /data/tags/{key}/  (named volume)
```

## Phase 0: the container runs the generator  [ ]

1. Replace the `Dockerfile`. This draft has not been built yet; check package names and the
   manifold3d wheel on the build box:

   ```dockerfile
   # Bag Tag: Go server + the Python/OpenSCAD generator. The whole gadget source is kept at /src.
   FROM golang:1.26-alpine AS build
   WORKDIR /src
   COPY go.mod ./
   COPY *.go ./
   COPY web ./web
   RUN go test ./... && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/app .

   FROM debian:trixie-slim
   RUN apt-get update && apt-get install -y --no-install-recommends \
         openscad python3 python3-numpy python3-shapely python3-fonttools python3-pip \
         fontconfig fonts-dejavu-core ca-certificates wget \
    && pip install --no-cache-dir --break-system-packages manifold3d \
    && apt-get purge -y python3-pip && apt-get autoremove -y && rm -rf /var/lib/apt/lists/*
   RUN useradd -u 1000 -m app && mkdir /data && chown app /data
   COPY --from=build /out/app /app
   COPY . /src
   ENV GENERATOR=/src/generator/bag_tag.py DATA=/data HOME=/home/app
   USER app
   EXPOSE 8080
   HEALTHCHECK --interval=30s --timeout=3s CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1
   ENTRYPOINT ["/app"]
   ```

   - **`go:embed`.** The Go stage only needs `*.go` and `web/`; don't embed `generator/`
     (it's read from `/src` at run time).
   - **If trixie's Python has no manifold3d wheel**, use `python:3.13-slim` as the base and
     apt-install only `openscad fontconfig fonts-dejavu-core`. manifold3d 3.5.4 installs as a
     wheel on CPython 3.13 x86_64 (checked).
2. Compose:
   - Keep `mem_limit` at about **768m** (2 concurrent builds × 200 MB, plus headroom).
   - Add a named volume `bag-tag-data:/data`.
   - No secrets, no host paths.
3. **Check** on the build box:

   ```
   docker build -t bag-tag:dev gadgets/bag-tag
   docker run --rm bag-tag:dev python3 /src/generator/bag_tag.py --name AUBREY --out /tmp/x --json
   ```

   - The info must say `"engine": "manifold"`, `length` 99.7, `width` 28.8, `inlay_pieces` 2.
     Those are the same numbers as `designing-stuff/bag-tag/info.json`.
   - `--list-fonts` must start with the 7 bundled fonts.
   - Note the image size in the README. OpenSCAD drags in Qt, so 500–700 MB is expected and
     acceptable on the PC.

## Phase 1: Go server  [ ]

Split across `main.go` (routes, config), `gen.go` (running the generator, cache) and
`api.go` (handlers + validation), plus tests.

- **Startup:**
  - Run `--list-fonts` and `--params` once and cache them; refresh fonts every hour.
  - Read the generator version: the "Vendored from:" line in `generator/SOURCE.md`, plus a
    short hash of `bag_tag.py` + `bag_tag.scad`. It goes into every cache key and `/api/version`.
- **Request** (JSON, both `/api/preview` and `/api/make`):

  ```json
  {"name": "Aubrey", "font": "Sniglet:style=ExtraBold", "letter_h": 12, "max_len": 62,
   "dir": "down", "options": {"flower": true, "clearance": 0.1}}
  ```

- **Validate in Go before exec:**
  - `name`: trimmed, 1–24 runes, no control characters.
  - `font`: must be in the cached list.
  - `letter_h`: 6–20.
  - `max_len`: 0 or 30–120.
  - `dir`: `down` or `up`.
  - `options`: keys must be in `--params`, excluding name/font/text_dir/text_size. Each value's
    type must match the default's type (bool, number, or string from `choices`); numbers between
    −180 and 200.
  - Map each option to `--set k=v`. Arguments go to `exec.CommandContext` as a list: never a shell.
- **Running the generator:**
  - A semaphore of 2 (env `CONCURRENCY`).
  - Timeout of 20 s for preview and 60 s for make. Return 503 "busy, try again" if the wait for
    a slot exceeds 10 s.
  - Exit 2 + `{"error"}` → **400** with that message. Timeout → **504**. Anything else → **500**,
    and log stderr.
- **Cache:**
  - The key is `sha256(version + canonical JSON of the validated request)`.
  - Previews go in `/data/previews/{key}/`, builds in `/data/tags/{key}/`; the id is the first
    16 hex characters of the key.
  - A repeated request returns instantly.
  - Clean up hourly: delete previews older than 1 day and builds older than 30 days (env
    `KEEP_DAYS`).
- **Responses:**
  - `/api/preview` → `{"info": {...}, "svg": "<svg…>"}`. Inline the SVG text; strip `files` and
    the generator's paths from `info`.
  - `/api/make` → `{"id", "info", "files": {"base_stl", "inlay_stl", "preview_svg", "info_json", "zip"}}`.
    The URLs are relative, `files/{id}/…`, so they work through the registry.
- **Downloads:**
  - Files are named `{stem}_base.stl` and so on. `.stl` and `.zip` are sent with
    `Content-Disposition: attachment`.
  - The zip is built in Go (`archive/zip`) on first request and then cached. It holds both STLs,
    the preview SVG, `info.json` and a `HOW-TO-PRINT.txt` written from the info.
  - `HOW-TO-PRINT.txt` covers:
    - The base prints pockets-up and the inlay prints face-down (`inlay_face_down`).
    - Material pairing and the clearance used: TPU base + PETG inlay at 0.1 is the default; a
      TPU inlay uses 0.
    - Press the flower in, then the name with its bar along the groove.
    - Any warnings.
- **Other routes:**
  - `GET /fonts/{file}` serves only the `.ttf` files in `generator/fonts` (an allowlist built at
    startup), with a long cache lifetime.
  - `GET /healthz` returns `ok`. `GET /healthz?deep=1` runs a cached `--preview-only` for "Test".
- **Tests** (`go test` runs in the alpine build stage, which has no Python):
  - Fake the generator with the helper-process pattern: the test binary re-execs itself when
    `GO_WANT_HELPER=1`, and the command comes from env `GENERATOR_CMD`.
  - Cover:
    - validation (each rejection)
    - the argument list built from a request
    - cache key stability (same request → same key; option order irrelevant)
    - error mapping (exit 2 → 400, timeout → 504)
    - the zip contents

## Phase 2: the page  [ ]

Embedded `web/index.html` + `app.js` + `app.css`, using `/_gadgets/ui.css` (`.card`, `.field`,
`.seg`, `.btn`, `var(--accent)`), so light/dark mode comes free. One column on a phone; on
desktop, the controls on the left and a sticky preview on the right.

1. **Name.** A big input, autofocused, with `maxlength=24`. Typing updates the preview, debounced
   400 ms, cancelling the previous request with an `AbortController`.
2. **Font.** A horizontal strip of cards, one per bundled font, each showing the typed name in
   that font. Use `@font-face` from `/fonts/…`; the family names come from `/api/fonts`. The
   selected card is highlighted. A "More fonts…" `<select>` lists the system fonts.
3. **Size.**
   - A segmented control: **Like the original** (letter_h 12, max_len 62; the default) | **Custom**.
   - Custom shows two sliders: letter height 8–16 mm and max name length 40–90 mm.
   - Under it, a line from the info, e.g. "99.7 × 28.8 × 6 mm · letters 10.8 mm".
4. **Reads:** a seg control, top → bottom (`down`) | bottom → top (`up`).
   **Flower:** on | off. **Inlay material:** PETG/PLA (clearance 0.1) | TPU (0).
5. **Advanced** (`<details>`): built from `/api/params`, grouped, with `help` as hint text,
   numbers as inputs with step 0.05, bools as switches, choices as seg controls.
   - Skip the parameters already on the page.
   - Add a "Reset to defaults" button.
6. **Preview card.**
   - The inline SVG, scaled to fit, with the AAA battery so the size reads at a glance.
   - Warnings appear as amber notes under it, and errors (e.g. missing characters) as red ones.
   - Show a small spinner while a request is in flight; never let it jump the layout.
7. **Make the files** (primary button) → calls `/api/make`, then shows:
   - three download buttons: **Base** (yellow swatch), **Inlay** (white swatch), **Both (.zip)**
   - a short print card from the info: base pockets-up, inlay face-down, which clearance was
     used, and "about 40–50 min + 20 min on the MK3S+ for a 6-letter name" (from the README's
     sliced times)
8. **Recent tags.** Keep the last 10 builds in `localStorage` (`try/catch`): name, font, id and
   date, with re-download links. If a link 404s (cache expired), quietly re-make it.
9. **Footer:**
   - "Model: designing-stuff/bag-tag" (link to GitHub)
   - "Gadget source" (link to GitHub `gadgets/bag-tag`; the code is also at `/src` in the
     container)
   - the generator version
10. **State in the URL query** (`?name=Aubrey&font=…`), so a tag can be shared or bookmarked,
    and an intent can deep-link to it.

## Phase 3: MCP  [ ]

Streamable HTTP, JSON responses, standard library: copy the shape of
`gadgets/quote-finder/mcp.go` (versions, `initialize`, `tools/list`, `tools/call`). Serve it at
`POST /mcp` on the same server. Plain `http://<pc>:8106/mcp` works through the registry because
each port speaks both protocols.

- **`list_fonts`** → the bundled fonts (and the count of system fonts, with an
  `include_system` flag to list them).
- **`preview_bag_tag`** `{name, font?, letter_h?, max_len?, direction?, options?}` → the info
  (size, letter height, warnings) and an absolute URL of the preview SVG.
- **`make_bag_tag`** with the same inputs → the info, plus absolute download URLs for the base,
  inlay and zip. Build the URLs from the request's `Host`, so they work from wherever the agent
  called.
- **Instructions string:**
  - What the tag is: two prints pressed together.
  - The defaults match the original (~100 × 29 × 6 mm).
  - Long names shrink to fit.
  - Pick a TPU inlay → `options.clearance = 0`.
  - Always pass on the warnings.
- **Tests:** `tools/list` returns the three tools; `make_bag_tag` with the fake generator
  returns URLs.

## Phase 4: test, document, deploy  [ ]

1. **`test/e2e.js`** (puppeteer-core on the build box), at 390×844 and 1280×800, light + dark:
   - Type "Aubrey" → the preview `<svg>` appears and the size line shows "× 28.8 × 6 mm".
   - Pick Titan One → the preview changes.
   - Turn on TPU inlay → the request carries `clearance: 0`.
   - Make the files → the three download buttons appear; fetch each URL, expect 200, and an
     STL size over 50 KB.
   - Type "🦄" → a red error, and no crash.
   - Expect no console errors.
   - Save screenshots to `/tmp/shots`.
2. **Golden check.** The default AUBREY made through `/api/make` must give `length` 99.7,
   `width` 28.8 and `inlay_pieces` 2, matching `designing-stuff/bag-tag/info.json`.
3. **`README.md`:**
   - what it does and how to use it
   - the API and MCP
   - how the generator is vendored (`generator/sync.sh`)
   - the image size
   - cache policy
4. **Top-level `README.md`:** add a row to the gadget table.
5. **Deploy and look:**
   - `tools/trinket check` → `tools/trinket deploy bag-tag` → `tools/trinket deploy registry`.
   - Open `https://<pc>:8106` on a phone-sized viewport through the registry: the navbar shows,
     and the "Making things" group appears in "I want to…".

**Done when:**
- From an iPhone you can type a name, see a to-scale preview within about a second, and
  download a base + inlay that slice in PrusaSlicer without errors.
- An agent can do the same over MCP.
- Re-running a request is instant.

## Later (only if the owner asks)

- **G-code in the box.** Run the PrusaSlicer CLI with `designing-stuff/printer-profiles`
  (eSun TPU 95A safe/FAST, Generic PETG, 0.20mm SPEED, avoid crossing perimeters) to offer
  ready `.gcode` and real print times.
  - It's a big image addition; check with the owner first.
  - `designing-stuff/bag-tag/slice_all.py` shows the preset flattening the 2.7 CLI needs.
- **A 3D preview** with a vendored three.js STL viewer; no CDN.
- **Font upload** (a TTF per request, kept with the build).
- **Generator features** (they belong in designing-stuff first):
  - other shapes (heart, star), a horizontal layout, two lines
  - a single-print raised-letter variant with an M600 colour change

## Kick-off prompt (for the Claude Code session on the PC)

> Read `gadgets/bag-tag/PLAN.md` in shiny-trinkets and build the Bag Tag gadget phase by phase.
> The geometry is done: `gadgets/bag-tag/generator/bag_tag.py` is the black box to wrap; don't
> change it here. Follow CLAUDE.md (build box, commit to master per phase, test at 390 px and
> 1280 px). Tick the boxes in PLAN.md as you go. Deploy with `tools/trinket deploy bag-tag` and
> then `tools/trinket deploy registry`. Ask me before adding G-code slicing or anything in "Later".
