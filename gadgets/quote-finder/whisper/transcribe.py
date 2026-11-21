"""One-off: transcribe videos Quote Finder has no subtitles for, and ingest the lines.

Env: INGEST_TOKEN, INDEX_URL (Quote Finder), MATCH (substring of the path), MODEL (ggml file),
THREADS. Media is mounted read-only at /media. Each file is sent with its real size and mtime,
so the extractor treats it as done and never overwrites it.
"""
import json, os, subprocess, sys, tempfile, time, urllib.request

URL = os.environ.get("INDEX_URL", "http://quote-finder:8080")
TOK = os.environ["INGEST_TOKEN"]
MATCH = os.environ.get("MATCH", "")
MODEL = os.environ.get("MODEL", "/models/ggml-small.en.bin")
THREADS = os.environ.get("THREADS", "6")
OUT_DIR = os.environ.get("OUT_DIR", "")  # if set, keep each SRT at OUT_DIR/<rel path>.srt
H = {"Authorization": "Bearer " + TOK, "Content-Type": "application/json"}

def call(method, path, body=None):
    req = urllib.request.Request(URL + path, method=method, headers=H,
                                 data=None if body is None else json.dumps(body).encode())
    with urllib.request.urlopen(req, timeout=120) as r:
        return json.loads(r.read() or b"null")

known = call("GET", "/api/ingest/known")
items = known.items() if isinstance(known, dict) else [(k["path"], k) for k in known]
todo = sorted(p for p, k in items if MATCH in p and k.get("status") in ("none", "error"))
print(f"{len(todo)} file(s) to transcribe", flush=True)
for i, rel in enumerate(todo, 1):
    src = os.path.join("/media", rel)
    st = os.stat(src)
    t0 = time.time()
    with tempfile.TemporaryDirectory() as d:
        wav = os.path.join(d, "a.wav")
        subprocess.run(["nice", "-n", "10", "ffmpeg", "-v", "error", "-i", src, "-map", "0:a:0", "-ac", "1",
                        "-ar", "16000", "-f", "wav", wav], check=True)
        subprocess.run(["nice", "-n", "10", "whisper-cli", "-m", MODEL, "-f", wav, "-t", THREADS, "-l", "en",
                        "-osrt", "-of", os.path.join(d, "out"), "-np"], check=True, stdout=subprocess.DEVNULL)
        srt = open(os.path.join(d, "out.srt"), encoding="utf-8", errors="replace").read()
    if OUT_DIR:
        dst = os.path.join(OUT_DIR, rel + ".srt")
        os.makedirs(os.path.dirname(dst), exist_ok=True)
        open(dst, "w", encoding="utf-8").write(srt)
    r = call("POST", "/api/ingest", {"path": rel, "size": st.st_size, "mtime": int(st.st_mtime),
                                     "status": "indexed", "source": "speech to text (whisper " +
                                     os.path.basename(MODEL).removeprefix("ggml-").removesuffix(".bin") + ")",
                                     "srt": srt})
    print(f"[{i}/{len(todo)}] {rel}: {r.get('lines')} lines in {time.time() - t0:.0f}s", flush=True)
print("done", flush=True)
