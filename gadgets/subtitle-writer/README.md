# Subtitle Writer

The only thing allowed to write into the media library. Machines that make subtitles somewhere
else (Whisper speech-to-text on the PC, translated copies of tracks; see
[quote-finder/whisper](../quote-finder/whisper)) hand them over, and it puts each one next to its
video so Plex offers it as a subtitle track. Runs on the Pi, next to the media, and does nothing
but receive and write small files.

- MCP: `http://<pi>:8131/mcp` (streamable HTTP). Tools: `put_subtitle`, `list_subtitles`,
  `remove_subtitle`.
- API: `POST /api/subtitles {video, srt, label, lang?}`, `GET /api/subtitles?match=&label=`,
  `DELETE /api/subtitles?path=`. `video` is the path relative to the media root, the same form
  Quote Finder uses (`TvShows/Show/Season 1/Show.S01E01.mkv`).
- Everything except `/healthz` needs `Authorization: Bearer <token>` (stack variable
  `SUBWRITER_TOKEN`; first deploy reads it from `~/.config/shiny-trinkets/subtitle-writer.env`).

      claude mcp add -s user -t http subtitle-writer http://<pi>:8131/mcp -H "Authorization: Bearer <token>"

## File names

`<video name>.<label>.<lang>.srt`, e.g. `Show.S01E01.whisper.en.srt` next to `Show.S01E01.mkv`,
and `Show.S01E01.translated.en.srt` for a translated copy.

Why this form: Plex picks up a sidecar subtitle when its name is the video's file name exactly,
followed by dot-separated parts with the language code last (`.en`, `.en.forced`, `.en.sdh`);
reports agree that an extra part before the language code (`.hi.eng.srt`) still shows up as
English, while anything after it (`.en.whisper.srt`) makes Plex skip the file or label it
Unknown. Putting `-whisper` straight after the video name (`Show.S01E01-whisper.en.srt`) changes
the video-name part, which the same rule says Plex won't match. So the label is its own part
before the language. In Plex it shows as another "English (SRT External)" track; Plex can't show
a custom title for an external file, so the label is only visible in the file name. If a Plex
version is found to match the dash form, `SUBWRITER_NAME_STYLE=dash` switches to
`<video name>-<label>.<lang>.srt` for new files.

Plex sees new files on its next scan of that folder (or "Scan Library Files").

## Safety

- Only `.srt` files, with names it builds itself (labels are `[a-z0-9]{1,24}`, languages 2-3
  letters); the caller never chooses a file name.
- Only next to a video that exists: a regular file with a video extension. The video and its
  folder are resolved with symlinks followed and must stay inside the media root; `..`, absolute
  paths and backslashes are refused.
- It never replaces or removes a file it didn't write. Every file it writes goes into a manifest
  (`/data/written.json`, volume `subtitle-writer-data`) with its SHA-256. Writing the same
  video + label + language again replaces its own earlier file, but only if it hasn't been
  edited since; `remove_subtitle` deletes only manifest files, also only if unchanged.
- Writes go to a temp file in the same folder, then a rename, so Plex never sees half a file.
- Runs as the media files' owner (uid 1000, gid 1001), read-only root filesystem, no
  capabilities, a quarter of a CPU, 32 processes. (The Pi's kernel has no memory cgroup, so
  Docker can't cap its memory; `GOMEMLIMIT` keeps the Go heap small instead. It idles at a few MB.)

## Working on it

    go vet ./... && go test ./...            # on the build box; tests cover the refusals
    tools/trinket deploy subtitle-writer     # cross-compiles on the build box, builds on the Pi
    tools/trinket deploy registry            # when gadget.json changes
