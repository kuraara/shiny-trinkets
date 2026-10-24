# Quote Finder

Search the dialogue of everything in Plex: type the bits of a line you remember and get the
show, episode and time, with the lines around it. Filter by show or movie.

- Words can be in any order and partly remembered ("likes" finds "like"); the last word
  matches as you type. `"exact words"` in quotes must appear together. If nothing has all the
  words, it shows matches with some of them.
- Tick **.\*** for a regular expression (case-insensitive), e.g. `just a little.*uneven`.
- A quote that's split across two subtitles still matches.
- Searches are in the URL, so they can be bookmarked or shared.

http://<pc>:8104 (through the gadget registry).

## How it works

Two parts:

- **`extractor/`** runs on the Pi, next to the media (`/path/to/video`, mounted read-only).
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
- **The server** (this folder, Go + SQLite FTS5) on the PC stores the lines and serves the
  search page and API (`/api/search`, `/api/context`, `/api/titles`, `/api/stats`). The
  extractor authenticates with `INGEST_TOKEN` (set on both stacks).

Show and episode come from the folder layout (`TvShows/<Show>/Season N/...S01E02...`,
`Movies/<Title (Year)>/...`); Plex itself isn't asked.

## Building

The Pi doesn't compile anything. On the build box:

    go test ./...
    cd extractor && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o extractor .

then build `extractor/Dockerfile` on the Pi (it only adds ffmpeg to Alpine). `test/e2e.js`
drives the search page in headless Chromium.
