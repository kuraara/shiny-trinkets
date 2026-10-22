#!/usr/bin/env python3
"""video-tools worker: analyse a clip for a panel transcript.

Transcribes (Whisper for text, Vosk for word timings), finds scene cuts, picks or takes frame times,
extracts frames, maps each line to a frame, measures voice pitch per line and builds contact sheets
that show who is talking.

usage: analyse.py CLIP OUTDIR [--times 1.0 2.5 7.2] [--scene-threshold 0.3]
  no --times -> auto: one frame per spoken line (middle of the line, inside its main shot);
                a silent clip gets one frame per shot.
env:   MODELS_DIR (default /data/models), ASR_THREADS (2), FFMPEG_THREADS (2), MAX_TRANSCRIPT_SECONDS (600)
out:   OUTDIR/analysis.json, frame_NN.png (+ _sm.jpg), contact_line_NN.jpg (+ _sm.jpg)
stdout: "PROGRESS <0-100> <message>" lines for the server, then a summary.
"""
import argparse, difflib, json, os, re, subprocess, sys, wave

MODELS = os.environ.get('MODELS_DIR', '/data/models')
WHISPER = os.path.join(MODELS, 'sherpa-onnx-whisper-small.en')
VOSK = os.path.join(MODELS, 'vosk-model-small-en-us-0.15')
ASR_THREADS = int(os.environ.get('ASR_THREADS', '2'))
FF_THREADS = os.environ.get('FFMPEG_THREADS', '2')
MAX_SECONDS = float(os.environ.get('MAX_TRANSCRIPT_SECONDS', '600'))


def progress(pct, msg):
    print(f'PROGRESS {pct} {msg}', flush=True)


def run(*a):
    return subprocess.run(a, capture_output=True, text=True)


def ffmpeg(*a):
    r = run('ffmpeg', '-nostdin', '-hide_banner', '-v', 'error', '-y', '-threads', FF_THREADS, *a)
    if r.returncode:
        raise RuntimeError('ffmpeg failed: ' + r.stderr.strip()[-500:])


def probe(clip):
    r = run('ffprobe', '-v', 'error', '-show_entries', 'format=duration:stream=codec_type', '-of', 'json', clip)
    if r.returncode:
        raise RuntimeError('not a readable video: ' + r.stderr.strip()[-300:])
    j = json.loads(r.stdout)
    return float(j['format']['duration']), any(s['codec_type'] == 'audio' for s in j['streams'])


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
    return [{'w': x['word'], 'start': round(x['start'], 2), 'end': round(x['end'], 2)} for x in out]


def whisper_text(wav, words, dur):
    """Whisper gives the cleanest text but no timings; decode in <=28 s chunks split at the widest pause."""
    import sherpa_onnx, soundfile as sf
    d = WHISPER + '/'
    rec = sherpa_onnx.OfflineRecognizer.from_whisper(
        encoder=d + 'small.en-encoder.int8.onnx', decoder=d + 'small.en-decoder.int8.onnx',
        tokens=d + 'small.en-tokens.txt', language='en', task='transcribe', num_threads=ASR_THREADS)
    cuts = [0.0]
    while dur - cuts[-1] > 28:
        s, best = cuts[-1], None
        for a, b in zip(words, words[1:]):
            if s + 12 < a['end'] < s + 28 and (best is None or b['start'] - a['end'] > best[0]):
                best = (b['start'] - a['end'], (a['end'] + b['start']) / 2)
        cuts.append(best[1] if best else s + 28)
    cuts.append(dur)
    texts = []
    with sf.SoundFile(wav) as f:                     # read one chunk at a time, not the whole file
        sr = f.samplerate
        for i, (a, b) in enumerate(zip(cuts, cuts[1:])):
            f.seek(int(a * sr))
            chunk = f.read(int((b - a) * sr), dtype='float32')
            st = rec.create_stream()
            st.accept_waveform(sr, chunk)
            rec.decode_stream(st)
            texts.append(st.result.text.strip())
            progress(30 + int(40 * (i + 1) / (len(cuts) - 1)), 'transcribing')
    text = ' '.join(t for t in texts if t)
    return re.sub(r'\s*[\[(][^\])]*[\])]\s*', ' ', text).strip()   # drop [MUSIC] / (laughs) tags


def norm(s):
    return re.sub(r'[^a-z0-9]', '', s.lower())


def split_lines(text, words):
    """Split Whisper text into sentences and time each one from the aligned Vosk words."""
    sents = [s.strip() for s in re.split(r'(?<=[.!?…])\s+', text) if s.strip()]
    wt, owner, first_tok, last_tok = [], [], {}, {}
    for i, s in enumerate(sents):
        for t in s.split():
            if norm(t):
                first_tok.setdefault(i, len(wt)); last_tok[i] = len(wt)
                wt.append(norm(t)); owner.append(i)
    vt = [norm(w['w']) for w in words]
    match = {}
    for b in difflib.SequenceMatcher(a=wt, b=vt, autojunk=False).get_matching_blocks():
        for k in range(b.size):
            match[b.a + k] = b.b + k
    vown = [None] * len(vt)
    for wi, vi in match.items():
        vown[vi] = owner[wi]
    # hand unmatched Vosk words to a neighbouring sentence, splitting runs by unmatched character share
    i = 0
    while i < len(vt):
        if vown[i] is not None:
            i += 1; continue
        j = i
        while j < len(vt) and vown[j] is None:
            j += 1
        p = vown[i - 1] if i > 0 else None
        n = vown[j] if j < len(vt) else None
        run_ = list(range(i, j))
        if p is None or n is None or p == n:
            for k in run_:
                vown[k] = p if p is not None else n
        else:
            tail = sum(len(wt[t]) for t in range(first_tok[p], last_tok[p] + 1) if t not in match and t > max(
                [m for m in match if owner[m] == p], default=-1))
            head = sum(len(wt[t]) for t in range(first_tok[n], last_tok[n] + 1) if t not in match and t < min(
                [m for m in match if owner[m] == n], default=10**9))
            if tail + head == 0:
                tail = head = 1
            target = sum(len(vt[k]) for k in run_) * tail / (tail + head)
            cum, best = 0, (abs(target), 0)
            for idx, k in enumerate(run_, 1):
                cum += len(vt[k])
                best = min(best, (abs(cum - target), idx))
            for idx, k in enumerate(run_):
                vown[k] = p if idx < best[1] else n
        i = j
    lines = []
    for i, s in enumerate(sents):
        ws = [w for w, o in zip(words, vown) if o == i]
        lines.append({'text': s, 'start': ws[0]['start'] if ws else None, 'end': ws[-1]['end'] if ws else None,
                      'timing': 'aligned' if ws else 'estimated'})
    for i, L in enumerate(lines):   # fill any untimed sentence from its neighbours
        if L['start'] is None:
            L['start'] = lines[i - 1]['end'] if i else 0.0
            L['end'] = next((x['start'] for x in lines[i + 1:] if x['start'] is not None),
                            words[-1]['end'] if words else L['start'] + 1)
    return lines


def scene_cuts(clip, thr):
    r = run('ffmpeg', '-nostdin', '-hide_banner', '-threads', FF_THREADS, '-i', clip, '-an', '-vf',
            f"select='gt(scene,{thr})',showinfo", '-f', 'null', '-')
    return [round(float(x), 3) for x in re.findall(r'pts_time:([0-9.]+)', r.stderr)]


def scene_of(t, cuts):
    return sum(1 for c in cuts if c <= t)


def auto_times(lines, cuts, dur):
    if not lines:   # silent: one frame per shot
        b = [0.0] + cuts + [dur]
        return [round((a + c) / 2, 2) for a, c in zip(b, b[1:])]
    out = []
    for L in lines:
        b = [L['start']] + [c for c in cuts if L['start'] < c < L['end']] + [L['end']]
        a, c = max(zip(b, b[1:]), key=lambda p: p[1] - p[0])      # longest piece inside one shot
        out.append(round((a + c) / 2, 2))
    return out


def voice_range(hz):
    if hz < 165:
        return 'low (adult male range)'
    if hz <= 300:
        return 'mid (adult female range)'
    return 'high (child range)'


def pitch(wav, lines):
    """Praat autocorrelation pitch per line. Rough speaker hint; FLAT means a tone/music is dominating."""
    try:
        import parselmouth, numpy as np
    except ImportError:
        return
    snd = parselmouth.Sound(wav)
    for L in lines:
        if L['end'] - L['start'] < 0.2:
            continue
        p = snd.extract_part(L['start'], L['end']).to_pitch_ac(time_step=0.01, pitch_floor=65, pitch_ceiling=600)
        f = p.selected_array['frequency']
        f = f[f > 0]
        if len(f) >= 5:
            p25, med, p75 = (float(np.percentile(f, q)) for q in (25, 50, 75))
            L['pitch_hz'] = {'median': round(med), 'p25': round(p25), 'p75': round(p75),
                             'flat_suspect': (p75 - p25) < 10, 'range': voice_range(med)}


def small(src, dst, width):
    from PIL import Image
    im = Image.open(src).convert('RGB')
    if im.width > width:
        im = im.resize((width, round(im.height * width / im.width)), Image.LANCZOS)
    im.save(dst, quality=82)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('clip'); ap.add_argument('outdir')
    ap.add_argument('--times', type=float, nargs='+')
    ap.add_argument('--scene-threshold', type=float, default=0.3)
    # scratch files; the server passes the job's work/ folder, which it empties even if this
    # process is killed (cancel, restart). Defaults to outdir for running the script by hand.
    ap.add_argument('--workdir')
    a = ap.parse_args()
    os.makedirs(a.outdir, exist_ok=True)
    a.workdir = a.workdir or a.outdir
    os.makedirs(a.workdir, exist_ok=True)
    progress(2, 'probing')
    dur, has_audio = probe(a.clip)
    if dur > MAX_SECONDS:
        raise RuntimeError(f'clip is {dur:.0f}s; the panel transcript limit is {MAX_SECONDS:.0f}s '
                           f'(MAX_TRANSCRIPT_SECONDS). Trim it first.')
    wav = os.path.join(a.workdir, 'audio.wav')
    words, lines, warnings = [], [], []
    if has_audio:
        progress(5, 'extracting audio')
        ffmpeg('-i', a.clip, '-vn', '-ac', '1', '-ar', '16000', wav)
        progress(10, 'word timings')
        words = vosk_words(wav)
        if words:   # skip Whisper on silence/music — it hallucinates there
            progress(30, 'transcribing')
            lines = split_lines(whisper_text(wav, words, dur), words)
            progress(72, 'voice pitch')
            pitch(wav, lines)
        else:
            warnings.append('no speech detected')
    else:
        warnings.append('clip has no audio track')
    progress(76, 'scene cuts')
    cuts = scene_cuts(a.clip, a.scene_threshold)
    bounds = [0.0] + cuts + [dur]
    for L in lines:   # shots the line really plays in (ignore audio that spills <0.25 s across a cut)
        ov = [(s, min(L['end'], b) - max(L['start'], a_)) for s, (a_, b) in enumerate(zip(bounds, bounds[1:]))]
        L['scenes'] = [s for s, o in ov if o >= 0.25] or [max(ov, key=lambda x: x[1])[0]]
    mode = 'user' if a.times else 'auto'
    times = [round(min(max(t, 0), dur - 0.05), 3) for t in (a.times or auto_times(lines, cuts, dur))]
    progress(82, 'frames')
    frames = []
    for i, t in enumerate(times, 1):
        p = os.path.join(a.outdir, f'frame_{i:02d}.png')
        ffmpeg('-ss', f'{t:.3f}', '-i', a.clip, '-frames:v', '1', '-update', '1', p)
        small(p, p.replace('.png', '_sm.jpg'), 640)
        frames.append({'t': t, 'image': os.path.basename(p), 'scene': scene_of(t, cuts), 'lines': []})
    # each line -> nearest frame in a shot the line touches (0 if the frame is inside the line; ties -> earlier)
    for li, L in enumerate(lines):
        c = [(fi, F) for fi, F in enumerate(frames) if F['scene'] in L['scenes']]
        if not c:
            c = list(enumerate(frames))
            warnings.append(f'line {li + 1} has no frame in its own shot; attached to the nearest frame')
        dist = lambda F: 0 if L['start'] <= F['t'] <= L['end'] else min(abs(F['t'] - L['start']), abs(F['t'] - L['end']))
        fi, F = min(c, key=lambda x: (dist(x[1]), x[0]))
        F['lines'].append(li)
        L['frame'] = fi
        if dist(F) > 0:
            warnings.append(f'line {li + 1} is spoken {L["start"]:.2f}-{L["end"]:.2f}s but shown on frame {fi + 1} '
                            f'({F["t"]:.2f}s, same shot)')
    for fi, F in enumerate(frames):
        if lines and not F['lines']:
            warnings.append(f'frame {fi + 1} ({F["t"]:.2f}s) has no line; it is mid-way through another line or silent')
    # speaker-check contact sheets: full frames (never crop — a cropped-out character can't be ruled out)
    progress(90, 'speaker check sheets')
    for li, L in enumerate(lines, 1):
        d = max(L['end'] - L['start'], 0.3)
        p = os.path.join(a.outdir, f'contact_line_{li:02d}.jpg')
        ffmpeg('-ss', f'{L["start"]:.3f}', '-t', f'{d:.3f}', '-i', a.clip,
               '-vf', f'fps={min(8 / d, 10):.3f},scale=480:-2,tile=2x4', '-frames:v', '1', '-update', '1', '-q:v', '4', p)
        small(p, p.replace('.jpg', '_sm.jpg'), 720)
        L['contact_sheet'] = os.path.basename(p)
    if os.path.exists(wav):
        os.remove(wav)                                   # 2 MB/min — no reason to keep it
    out = {'duration': round(dur, 3), 'mode': mode, 'scene_cuts': cuts, 'lines': lines,
           'frames': frames, 'words': words, 'warnings': warnings}
    with open(os.path.join(a.outdir, 'analysis.json'), 'w') as f:
        json.dump(out, f, indent=1, ensure_ascii=False)
    progress(100, 'done')
    print(f'duration {dur:.2f}s  scene cuts {cuts}  mode {mode}')
    for i, L in enumerate(lines, 1):
        ph = L.get('pitch_hz')
        ps = f"  pitch {ph['median']}Hz {ph['range']}{' FLAT?' if ph['flat_suspect'] else ''}" if ph else ''
        print(f"line {i}: {L['start']:.2f}-{L['end']:.2f}s shot {L['scenes']} -> frame {L['frame'] + 1}{ps}  {L['text']}")
    for w in warnings:
        print('WARN', w)


if __name__ == '__main__':
    try:
        main()
    except Exception as e:                              # one clean line for the server to show
        print(f'ERROR {e}', flush=True)
        sys.exit(1)
