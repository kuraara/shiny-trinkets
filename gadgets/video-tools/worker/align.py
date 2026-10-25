#!/usr/bin/env python3
"""video-tools worker: line known dialogue (e.g. subtitles) up with what is actually said.

Subtitles get you close, but they can be early or late (a different release, a fetched file) and
their end times are often short. This runs Vosk word timings over the clip's audio, matches the
subtitle words to the recognised words, and moves each line to where it is really spoken.

usage: align.py CLIP OUT.json --lines lines.json [--first N --last M] [--lead 0.5 --tail 0.7]
  lines.json: [{text, start, end}] in seconds from the start of CLIP, in order. Lines outside
  first..last are context: they help find the overall offset and keep a cut from running into
  the neighbouring lines.
out:  {"lines": [{text, start, end, matched, words}], "offset": s, "matched": n, "words": n,
       "cut": {"start": s, "end": s}}   cut = where to start and end a clip of first..last
stdout: "PROGRESS <0-100> <message>" lines, "ERROR <message>" on failure.
env:  MODELS_DIR (default /data/models), FFMPEG_THREADS (2)
"""
import argparse, difflib, json, os, re, statistics, subprocess, sys, wave

MODELS = os.environ.get('MODELS_DIR', '/data/models')
VOSK = os.path.join(MODELS, 'vosk-model-small-en-us-0.15')
FF_THREADS = os.environ.get('FFMPEG_THREADS', '2')
WORD = 0.32          # typical length of a spoken word, for words that weren't recognised


def progress(pct, msg):
    print(f'PROGRESS {pct} {msg}', flush=True)


def norm(s):
    return re.sub(r'[^a-z0-9]', '', s.lower())


def extract_wav(clip, wav):
    """16 kHz mono, with t=0 at the container's t=0 (gaps and a late audio start are padded)."""
    r = subprocess.run(['ffmpeg', '-nostdin', '-hide_banner', '-v', 'error', '-y', '-threads', FF_THREADS,
                        '-i', clip, '-vn', '-af', 'aresample=async=1:first_pts=0', '-ac', '1', '-ar', '16000', wav],
                       capture_output=True, text=True)
    if r.returncode:
        raise RuntimeError('ffmpeg failed: ' + r.stderr.strip()[-400:])


def vosk_words(wav):
    import vosk
    vosk.SetLogLevel(-1)
    wf = wave.open(wav, 'rb')
    rec = vosk.KaldiRecognizer(vosk.Model(VOSK), wf.getframerate())
    rec.SetWords(True)
    out = []
    while True:
        d = wf.readframes(4000)
        if not d:
            break
        if rec.AcceptWaveform(d):
            out += json.loads(rec.Result()).get('result', [])
    out += json.loads(rec.FinalResult()).get('result', [])
    return [{'w': x['word'], 'start': x['start'], 'end': x['end']} for x in out]


def align(words, lines):
    """Returns (lines with start/end moved to the speech, median offset, words matched)."""
    toks = []                                # (line index, position in line, normalised word)
    for li, L in enumerate(lines):
        ws = [norm(t) for t in L['text'].split()]
        ws = [w for w in ws if w]
        for k, w in enumerate(ws):
            toks.append((li, k, w))
    counts = [sum(1 for t in toks if t[0] == li) for li in range(len(lines))]
    vt = [norm(w['w']) for w in words]
    sm = difflib.SequenceMatcher(a=[t[2] for t in toks], b=vt, autojunk=False)
    blocks = [b for b in sm.get_matching_blocks() if b.size]

    # expected time of a token from its line's subtitle timing (spread evenly over the line)
    def sub_time(i):
        li, k, _ = toks[i]
        L = lines[li]
        return L['start'] + (L['end'] - L['start']) * (k + 0.5) / max(1, counts[li])

    # pass 1: runs of 2+ words are trustworthy; they give the overall offset
    offs = [words[b.b + k]['start'] - sub_time(b.a + k) for b in blocks if b.size >= 2 for k in range(b.size)]
    offset = statistics.median(offs) if offs else 0.0
    # pass 2: keep every matched word that lands near where the offset says it should
    match = {}
    for b in blocks:
        for k in range(b.size):
            i, j = b.a + k, b.b + k
            if abs(words[j]['start'] - (sub_time(i) + offset)) <= (2.5 if b.size >= 2 else 1.2):
                match[i] = j

    out = []
    for li, L in enumerate(lines):
        idx = [i for i, t in enumerate(toks) if t[0] == li]
        got = [i for i in idx if i in match]
        if got:
            first, last = got[0], got[-1]
            start = words[match[first]]['start'] - WORD * toks[first][1]               # words before the first match
            end = words[match[last]]['end'] + WORD * (counts[li] - 1 - toks[last][1])  # words after the last match
        else:
            start, end = L['start'] + offset, L['end'] + offset
        out.append({'text': L['text'], 'start': round(max(0.0, start), 3), 'end': round(max(start + 0.2, end), 3),
                    'matched': len(got), 'words': counts[li], 'subtitle_start': L['start']})
    return out, round(offset, 3), len(match)


PAUSE = 0.25         # a gap in speech at least this long ends a run of words
MIN_LEAD = 0.15      # always this much before the first word…
MIN_TAIL = 0.2       # …and after the last


def cut(aligned, words, first, last, lead, tail, duration):
    """Start/end for a clip of lines first..last: a little outside the speech, not into the
    neighbouring lines. Speech that carries on without a pause past a line's (estimated) end
    still belongs to it, so a misheard last word isn't cut off."""
    lo = aligned[first - 1]['end'] if first > 0 else None          # previous line ends
    hi = aligned[last + 1]['start'] if last + 1 < len(aligned) else None  # next line starts
    # extend the end through continuous speech, stopping at the next line
    end = aligned[last]['end']
    for w in words:
        if w['start'] < end - 0.01:
            continue
        if hi is not None and w['start'] >= hi - 0.05:
            break
        if w['start'] - end > PAUSE:
            break
        end = max(end, w['end'])
    # and the start back through continuous speech, stopping at the previous line
    start = aligned[first]['start']
    for w in reversed(words):
        if w['end'] > start + 0.01:
            continue
        if lo is not None and w['end'] <= lo + 0.05:
            break
        if start - w['end'] > PAUSE:
            break
        start = min(start, w['start'])
    s, e = start - lead, end + tail
    # no more than half the gap into the neighbouring speech
    before = [w['end'] for w in words if w['end'] <= start + 0.01] + ([lo] if lo is not None else [])
    if before and max(before) > s:
        s = (max(before) + start) / 2
    after = [w['start'] for w in words if w['start'] >= end - 0.01] + ([hi] if hi is not None else [])
    if after and min(after) < e:
        e = (min(after) + end) / 2
    # but never so tight that the first or last word sounds clipped (the clip fades its sound
    # in and out over ~0.1 s, so a sliver of the neighbouring line isn't heard)
    s, e = min(s, start - MIN_LEAD), max(e, end + MIN_TAIL)
    return {'start': round(max(0.0, s), 3), 'end': round(min(duration, max(e, s + 0.5)), 3),
            'speech_start': round(start, 3), 'speech_end': round(end, 3)}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('clip'); ap.add_argument('out')
    ap.add_argument('--lines', required=True)
    ap.add_argument('--first', type=int, default=0)
    ap.add_argument('--last', type=int, default=-1)
    ap.add_argument('--lead', type=float, default=0.5)
    ap.add_argument('--tail', type=float, default=0.7)
    ap.add_argument('--workdir')
    a = ap.parse_args()
    lines = json.load(open(a.lines))
    if not lines:
        raise RuntimeError('no lines to align')
    last = a.last if a.last >= 0 else len(lines) - 1
    work = a.workdir or os.path.dirname(os.path.abspath(a.out))
    wav = os.path.join(work, 'align.wav')
    progress(5, 'reading the audio')
    extract_wav(a.clip, wav)
    with wave.open(wav, 'rb') as wf:
        duration = wf.getnframes() / wf.getframerate()
    progress(20, 'listening for the words')
    words = vosk_words(wav)
    os.remove(wav)
    progress(90, 'lining up the subtitles')
    aligned, offset, n = align(words, lines)
    res = {'lines': aligned, 'offset': offset, 'matched': n, 'words': len(words),
           'cut': cut(aligned, words, a.first, last, a.lead, a.tail, duration)}
    with open(a.out, 'w') as f:
        json.dump(res, f, indent=1)
    progress(100, 'done')
    print(f"offset {offset:+.2f}s, {n} subtitle words matched of {sum(L['words'] for L in aligned)}; "
          f"cut {res['cut']['start']:.2f}-{res['cut']['end']:.2f}")


if __name__ == '__main__':
    try:
        main()
    except Exception as e:
        print(f'ERROR {e}', flush=True)
        sys.exit(1)
