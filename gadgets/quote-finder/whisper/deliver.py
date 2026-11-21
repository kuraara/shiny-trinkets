"""Hand finished subtitles to Subtitle Writer on the Pi, which puts them next to the videos.

Reads SRC_DIR/<video path>.srt (the layout transcribe.py and translate.py write) and posts each
to WRITER_URL/api/subtitles with LABEL ("whisper", "translated") and LANG. Files whose path
doesn't contain MATCH are skipped. Env: SUBWRITER_TOKEN, WRITER_URL, SRC_DIR, LABEL, LANG, MATCH.
"""
import json, os, sys, urllib.error, urllib.request

URL = os.environ["WRITER_URL"].rstrip("/")
TOK = os.environ["SUBWRITER_TOKEN"]
SRC = os.environ.get("SRC_DIR", "/out")
LABEL = os.environ["LABEL"]
LANG = os.environ.get("LANG_CODE", "en")
MATCH = os.environ.get("MATCH", "")

ok = bad = 0
for d, _, files in sorted(os.walk(SRC)):
    for f in sorted(files):
        if not f.endswith(".srt"):
            continue
        full = os.path.join(d, f)
        video = os.path.relpath(full, SRC)[:-len(".srt")]
        if MATCH not in video:
            continue
        body = json.dumps({"video": video, "srt": open(full, encoding="utf-8").read(), "label": LABEL, "lang": LANG}).encode()
        req = urllib.request.Request(URL + "/api/subtitles", data=body, method="POST",
                                     headers={"Authorization": "Bearer " + TOK, "Content-Type": "application/json"})
        try:
            with urllib.request.urlopen(req, timeout=60) as r:
                e = json.loads(r.read())
            ok += 1
            print(f"ok   {e['path']} ({e['size']} bytes{', replaced' if e.get('replaced') else ''})", flush=True)
        except urllib.error.HTTPError as e:
            bad += 1
            print(f"FAIL {video}: {e.code} {e.read()[:300].decode(errors='replace')}", flush=True)
print(f"{ok} written, {bad} failed", flush=True)
sys.exit(1 if bad else 0)
