# Quote Finder

Search the dialogue of everything in Plex: type the bits of a line you remember and get the
show, episode and time, with the lines around it. Filter by show or movie.

- Words can be in any order and partly remembered ("likes" finds "like"); the last word
  matches as you type. `"exact words"` in quotes must appear together. If nothing has all the
  words, it shows matches with some of them.
- Tick **.\*** for a regular expression (case-insensitive), e.g. `just a little.*uneven`.
- A quote that's split across two subtitles still matches.
- Searches are in the URL, so they can be bookmarked or shared.
- **Panels or clip…** on a result: tap the first and last line of the scene, then
  - **Make panels**: video-tools takes the frames for the subtitle lines (no speech recognition),
    then Quote Finder's **panel editor** (`panel.html?job=`) opens: name the speakers (with
    video-tools' "who's talking" frame strips and voice pitch), and lay the panels out with
    Photo Stitch's controls (panel shape, columns, gap and margin in % or px, background, image
    width, subtitle size, optional 1/4 panel numbers, PNG/JPEG). The image is drawn and saved in
    the browser, the same way video-tools draws it; names are sent back to video-tools too;
  - **Make clip**: video-tools cuts the scene (a second either side) into an MP4 that plays
    anywhere, optionally with the subtitles burned in.

`https://<pc>:8104` (through the gadget registry). Machines use the direct, plain-HTTP port
8124: the extractor's ingest and agents' MCP (`http://<pc>:8124/mcp`).

## How it works

Two parts:

- **`extractor/`** runs on the Pi, next to the media (`VIDEO_DIR` in `registry/site.local.json`,
  mounted read-only).
  It walks the library, picks the best English *text* subtitle track in each file (full over
  forced/signs, plain over SDH), pulls it out with ffmpeg at the lowest CPU and disk priority,
  one file at a time, and sends it to the server. Rescans every 6 hours for new files and
  forgets deleted ones. `ONLY` limits it to some folders (e.g. `TvShows/Mike Tyson Mysteries`).
  Files go smallest first, and the extractor measures the disk's read speed as it goes.
  **If a file would take more than a minute to read, it asks OpenSubtitles first** (matching
  the exact release by its OpenSubtitles hash, which only reads 128 KB), and only reads the
  whole file if that fails, at the end of the pass. Files with no English text subtitles
  (none, or picture-only Blu-ray PGS) also get an OpenSubtitles try, as far as the daily
  download allowance goes (about 20 a day on a free account). OpenSubtitles is off until
  `OPENSUBTITLES_API_KEY`, `_USERNAME` and `_PASSWORD` are set on the extractor stack.
  It also serves **scene clips** on port 8105 (`/clip?path=&start=&end=`, same token): the
  stretch is copied out without re-encoding, so the Pi barely notices. A copy can only start
  on a keyframe, so the `X-Clip-Start` header says where it really starts.
- **The server** (this folder, Go + SQLite FTS5) on the PC stores the lines and serves the
  search page and API (`/api/search`, `/api/context`, `/api/titles`, `/api/stats`,
  `POST /api/panels {media, from, to, clip?, subtitles?}`). The extractor authenticates with
  `INGEST_TOKEN` (set on both stacks). For panels and clips it streams the scene from the
  extractor (`CLIP_URL`) straight into video-tools (`VIDEO_TOOLS_URL`; links use
  `VIDEO_TOOLS_PUBLIC_URL`; `VIDEO_TOOLS_TOKEN` if video-tools has an `AUTH_TOKEN`).

## Agents (MCP)

`http://<pc>:8124/mcp` (streamable HTTP). Tools: `search_quotes`, `get_scene`,
`make_panel_script`, `make_clip`, `list_titles`. Pair it with the video-tools MCP to name the
speakers of a panel script or fetch a finished clip:

    claude mcp add --transport http quote-finder http://<pc>:8124/mcp
    claude mcp add --transport http video-tools http://<pc>:8090/mcp

Show and episode come from the folder layout (`TvShows/<Show>/Season N/...S01E02...`,
`Movies/<Title (Year)>/...`); Plex itself isn't asked.

## Building

The Pi doesn't compile anything. On the build box:

    go test ./...
    cd extractor && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o extractor .

then build `extractor/Dockerfile` on the Pi (it only adds ffmpeg to Alpine). `test/e2e.js`
drives the search page in headless Chromium; `test/panels.e2e.js` makes panels and a clip
from a real scene at phone size.
