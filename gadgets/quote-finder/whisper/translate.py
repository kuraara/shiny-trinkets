"""Translated duplicates of subtitle tracks that hide foreign dialogue behind placeholders.

For each file with placeholder cues ("[speaking Spanish]" with no translation): pull the file's
original subtitle track (the same track Quote Finder indexed), cut each placeholder cue's audio
(+-0.5 s), translate it to English with Whisper (-tr, multilingual model) and write a COMPLETE
copy of the track in which only those cues get the translation after the original bracket.
Every other cue stays byte-identical; this is checked before the file is saved.

In: /in.json (rows: media_id, title, kind, path, source, idx, start_ms, end_ms, text, full|partial)
Out: OUT_DIR/<path>.srt and OUT_DIR/report.json. Env: MODEL, THREADS, OUT_DIR, LIMIT, ONLY.
REUSE=<an earlier report.json>: take single-speaker cues' translations from it instead of running
Whisper again (cues shared with English speech are always redone).
"""
import collections, difflib, json, os, re, subprocess, tempfile, time

MODEL = os.environ.get("MODEL", "/models/ggml-medium.bin")
THREADS = os.environ.get("THREADS", "2")
OUT = os.environ.get("OUT_DIR", "/out/translated")
LIMIT = int(os.environ.get("LIMIT", "0"))
ONLY = os.environ.get("ONLY", "")
REUSE = os.environ.get("REUSE", "")
# Translations dropped after reading them all (hallucinations, garbage, and two lines in a
# children's episode that came out wrong): (file name contains, cue start ms).
DROP = {("Cross.2024.S02E06", 1940147), ("DuckTales.2017-S02E14", 114750), ("Beavis.And.Butt.Head.S04E01", 96868),
        ("Peppa.Pig.S07E33", 223990), ("Peppa.Pig.S07E33", 256923), ("The.Expanse.S01E01", 1583804),
        ("The.Westies.2026.S01E04", 2941981),
        # fictional languages (Huttese/Tusken, Belter): whatever Whisper makes of them isn't a translation
        ("The.Book.of.Boba.Fett.S01E02", None), ("The.Expanse.S01E01", None)}
NICE = ["nice", "-n", "15"]
# Bracket word -> Whisper language; anything else (and Irish/Gaelic, which Whisper lacks) is "auto".
LANG = {"spanish": "es", "french": "fr", "german": "de", "italian": "it", "russian": "ru", "korean": "ko",
        "japanese": "ja", "chinese": "zh", "mandarin": "zh", "cantonese": "yue", "arabic": "ar", "hindi": "hi",
        "portuguese": "pt", "indonesian": "id", "hawaiian": "haw",
        "swedish": "sv", "norwegian": "no"}
HALLU = re.compile(r"^(thank you\.?|thanks for watching!?|you\.?|bye\.?|\.+|subtitles? by .*|\[.*\]|\(.*\)|♪.*|-)$", re.I)
TIME = re.compile(r"(\d+):(\d\d):(\d\d)[,.](\d{1,3})\s*-->\s*(\d+):(\d\d):(\d\d)[,.](\d{1,3})")
TAG = re.compile(r"<[^>]*>|\{[^}]*\}")
BR = re.compile(r"[\[(][^\])]*[\])]")


def ms(h, m, s, f):
    return ((int(h) * 60 + int(m)) * 60 + int(s)) * 1000 + int(f.ljust(3, "0")[:3])


def clean(s):  # as Quote Finder's cleanCue
    s = s.replace("\\N", " ").replace("\\n", " ").replace("\\h", " ")
    return " ".join(TAG.sub("", s).split())


def blocks(srt):
    """Split SRT into raw blocks: raw text, lines, start ms, index of the first text line."""
    out = []
    for raw in re.split(r"\n\n+", srt.replace("\r\n", "\n").strip("\n")):
        lines = raw.split("\n")
        st = ti = None
        for i, l in enumerate(lines):
            m = TIME.search(l)
            if m:
                st, ti = ms(*m.groups()[:4]), i + 1
                break
        out.append({"raw": raw, "lines": lines, "start": st, "ti": ti})
    return out


def english_audio(path):
    """The first English audio stream (MULTi releases often put a dub first), else the first."""
    pr = json.loads(subprocess.run(["ffprobe", "-v", "error", "-select_streams", "a", "-show_entries",
                                    "stream=index:stream_tags=language", "-of", "json", path],
                                   check=True, capture_output=True).stdout or b"{}")
    st = pr.get("streams", [])
    for s in st:
        if (s.get("tags") or {}).get("language", "").lower() in ("eng", "en"):
            return f"0:{s['index']}"
    return f"0:{st[0]['index']}" if st else "0:a:0"


def translate(path, start, end, lang, amap):
    a, d = max(0, start - 500) / 1000, (end - start + 1000) / 1000
    with tempfile.TemporaryDirectory() as td:
        wav = os.path.join(td, "a.wav")
        subprocess.run(NICE + ["ffmpeg", "-nostdin", "-v", "error", "-ss", f"{a:.3f}", "-t", f"{d:.3f}", "-i", path,
                               "-map", amap, "-ac", "1", "-ar", "16000", wav], check=True)
        subprocess.run(NICE + ["whisper-cli", "-m", MODEL, "-f", wav, "-t", THREADS, "-tr", "-l", lang, "-nt", "-np",
                               "-otxt", "-of", os.path.join(td, "o")],
                       check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        o = os.path.join(td, "o.txt")
        if not os.path.exists(o):  # e.g. a language Whisper doesn't know
            return ""
        return " ".join(open(o, encoding="utf-8", errors="replace").read().split())


def lang_of(bracket):
    for k, v in LANG.items():
        if k in bracket.lower():
            return v
    return "auto"


FOREIGN = re.compile(r"non[- ]?english|foreign|spanish|french|german|italian|russian|korean|japanese|chinese|mandarin|"
                     r"cantonese|arabic|hindi|portuguese|indonesian|gaelic|hawaiian|creole|native language|"
                     r"other language|own language|another language", re.I)


def is_placeholder_line(l):
    """A line that is only brackets, one of them a foreign-speech note ("[laughter]" alone isn't)."""
    l = TAG.sub("", l)
    return any(FOREIGN.search(b) for b in BR.findall(l)) and not BR.sub("", l).strip(" -–—.,…:")


def norm(s):
    return re.sub(r"\W+", " ", s).strip().lower()


def tidy(tr):
    """Drop Whisper's own notes ("(speaking in foreign language)", "[music]") from a translation."""
    return " ".join(BR.sub(" ", tr).split()).strip()


def strip_english(tr, english):
    """Whisper also hears the English the other speaker says in a shared cue: remove it when the
    translation starts with (something close to) it. Returns "" if nothing else is left."""
    t = tr.lstrip(" -–—")
    for o in english:
        words = norm(o).split()
        if not words:
            continue
        toks = list(re.finditer(r"\w+", t))
        if len(toks) < len(words):
            if difflib.SequenceMatcher(None, norm(t), " ".join(words)).ratio() >= 0.75:
                return ""
            continue
        head = " ".join(m.group(0).lower() for m in toks[:len(words)])
        if difflib.SequenceMatcher(None, head, " ".join(words)).ratio() >= 0.75:
            t = t[toks[len(words) - 1].end():].lstrip(" .,!?-–—")
    return t.strip()


rows = json.load(open("/in.json"))
byfile = collections.OrderedDict()
for r in sorted(rows, key=lambda r: (r[3], r[6])):
    if ONLY in r[3]:
        byfile.setdefault(r[3], []).append(r)
os.makedirs(OUT, exist_ok=True)
prev = {}
if REUSE:
    for x in json.load(open(REUSE)):
        for c in x["cues"]:
            prev[(x["path"], c["at_ms"])] = c
rp = os.path.join(OUT, "report.json")
report = json.load(open(rp)) if os.path.exists(rp) else []
seen = {x["path"] for x in report}
n = 0
for path, rs in byfile.items():
    if path in seen:
        continue
    if LIMIT and n >= LIMIT:
        break
    n += 1
    t0, src = time.time(), "/media/" + path
    tidx = int(re.match(r"track (\d+)", rs[0][4]).group(1))
    orig = subprocess.run(NICE + ["ffmpeg", "-nostdin", "-v", "error", "-i", src, "-map", f"0:{tidx}", "-f", "srt", "-"],
                          check=True, capture_output=True).stdout.decode("utf-8", "replace")
    bl = blocks(orig)
    amap = english_audio(src)
    rec = {"path": path, "title": rs[0][1], "audio": amap, "cues": [], "status": "ok"}
    changed = {}
    for r in rs:
        cand = [i for i, b in enumerate(bl) if b["start"] == r[6] and b["ti"] is not None]
        cand = [i for i in cand if clean(" ".join(bl[i]["lines"][bl[i]["ti"]:])) == r[8]] or cand
        if not cand:
            rec["cues"].append({"at_ms": r[6], "text": r[8], "result": "cue not found in track"})
            continue
        i = cand[0]
        if i in changed:
            continue
        b = bl[i]
        tl = b["lines"][b["ti"]:]
        hold = [k for k, l in enumerate(tl) if is_placeholder_line(l)]
        if r[9] == "partial" and not hold:
            rec["cues"].append({"at_ms": r[6], "text": r[8], "result": "placeholder line not found; left as is"})
            continue
        lang = lang_of(" ".join(BR.findall(r[8])))
        if any(f in path and ms_ in (r[6], None) for f, ms_ in DROP):
            rec["cues"].append({"at_ms": r[6], "text": r[8], "lang": lang, "result": "dropped after review; left as is"})
            continue
        p = prev.get((path, r[6]))
        if r[9] == "full" and p and p["result"] == "translated":
            tr = p["translation"]
        elif r[9] == "full" and p and p["result"].startswith("no usable"):
            rec["cues"].append(dict(p))
            continue
        else:
            try:
                tr = translate(src, r[6], r[7], lang, amap)
            except subprocess.CalledProcessError as e:
                rec["cues"].append({"at_ms": r[6], "text": r[8], "result": f"whisper failed: {e}"})
                continue
        raw = tr
        if r[9] == "partial":
            tr = strip_english(tr, [clean(l) for k, l in enumerate(tl) if k not in hold])
        tr = tidy(tr)
        if not tr or HALLU.match(tr.strip()) or not re.search(r"\w", tr):
            rec["cues"].append({"at_ms": r[6], "text": r[8], "lang": lang, "result": f"no usable translation ({raw!r}); left as is"})
            continue
        new = list(b["lines"])
        k = b["ti"] + (hold[0] if hold else len(tl) - 1)
        new[k] = new[k].rstrip() + " " + tr
        changed[i] = "\n".join(new)
        rec["cues"].append({"at_ms": r[6], "text": r[8], "lang": lang, "result": "translated", "translation": tr})
    out_blocks = [changed.get(i, b["raw"]) for i, b in enumerate(bl)]
    # The check: same number of cues, every cue not translated byte-identical, and the
    # translated ones keep their number and timing lines.
    nb = blocks("\n\n".join(out_blocks))
    same = sum(1 for i, b in enumerate(bl) if i not in changed and i < len(nb) and nb[i]["raw"] == b["raw"])
    ok = (len(nb) == len(bl) and same == len(bl) - len(changed)
          and all(nb[i]["lines"][:nb[i]["ti"]] == bl[i]["lines"][:bl[i]["ti"]] for i in changed))
    rec.update({"orig_cues": len(bl), "out_cues": len(nb), "unchanged_identical": same, "translated": len(changed),
                "check": ok, "secs": round(time.time() - t0)})
    if not changed:
        rec["status"] = "nothing translated"
    elif not ok:
        rec["status"] = "CHECK FAILED"
    else:
        dst = os.path.join(OUT, path + ".srt")
        os.makedirs(os.path.dirname(dst), exist_ok=True)
        open(dst, "w", encoding="utf-8").write("\n\n".join(out_blocks) + "\n")
    report.append(rec)
    json.dump(report, open(rp, "w"), indent=1)
    print(f"[{n}/{len(byfile)}] {path}: {len(changed)}/{len(rs)} translated, cues {len(bl)}->{len(nb)}, "
          f"check {ok}, {rec['secs']}s", flush=True)
print("done", flush=True)
