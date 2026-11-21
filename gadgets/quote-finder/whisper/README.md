# Whisper tools (speech to text, translated subtitles)

One-off jobs run on the build box (the PC's Docker), image `qf-whisper:latest`: whisper.cpp's
`whisper-cli` + ffmpeg + Python. They read the library through the extractor's read-only SMB
volume (`subtitle-extractor-pc_pi-video`, mounted at `/media`), never touch the Pi's CPU, and
hand finished subtitles to [Subtitle Writer](../../subtitle-writer) on the Pi, which puts them
next to the videos.

    docker build -t qf-whisper:latest .        # whisper.cpp is compiled in a throwaway stage
    # models in the qf-whisper-models volume: ggml-small.en.bin (English), ggml-medium.bin (multilingual)

| Script | Does |
|---|---|
| `transcribe.py` (entrypoint) | For every file Quote Finder knows with `MATCH` in its path and no usable subtitles (status none/error): Whisper (`MODEL`, default small.en, `THREADS`) on its first audio track, ingests the lines into Quote Finder (source "speech to text (whisper …)", with the file's real size and mtime so the extractor leaves it alone), and with `OUT_DIR` keeps each SRT as `OUT_DIR/<video path>.srt`. Needs `INGEST_TOKEN`. |
| `translate.py` | Translated copies of tracks that hide foreign dialogue behind placeholders ("[speaking Spanish]" with no translation). Input `/in.json`: the placeholder cues found in Quote Finder's DB. For each file it pulls the same subtitle track the extractor indexed, cuts each placeholder cue's audio (±0.5 s, English audio track), translates it with `whisper-cli -tr` (multilingual model, language from the bracket) and writes a **complete** copy of the track where only those cues gain the translation after the original bracket ("[speaking Spanish] Get out!"). Before saving it checks the copy has the same number of cues and every other cue is byte-identical. Whisper's usual hallucinations on short clips ("Thank you.") are dropped, leaving that cue as it was. `report.json` lists every cue's result. |
| `deliver.py` | Posts `SRC_DIR/<video path>.srt` files to Subtitle Writer (`WRITER_URL`, `SUBWRITER_TOKEN`, `LABEL`, `LANG_CODE`). |

Examples (on the build box; tokens in env files, never on the command line):

    docker run -d --name qf-whisper-x --network gadgets --env-file token.env -e "MATCH=Ben and Holly" \
      -e THREADS=6 -e OUT_DIR=/out -v qf-whisper-models:/models:ro \
      -v subtitle-extractor-pc_pi-video:/media:ro -v qf-whisper-out:/out qf-whisper:latest
    docker run --rm --env-file subwriter.env -e WRITER_URL=http://<pi>:8131 -e LABEL=whisper \
      -e SRC_DIR=/out -v qf-whisper-out:/out --entrypoint python3 qf-whisper:latest -u /deliver.py

Speed on the PC: small.en does an 11-minute episode in about 4 minutes on 6 threads; medium
translates a placeholder cue in 30–60 s on 2 threads. Translations are machine output from a few
seconds of audio: good enough to follow the scene, not polished subtitles.
