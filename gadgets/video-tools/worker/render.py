#!/usr/bin/env python3
"""video-tools worker: stack frames vertically on black with burned-in subtitles  Name: “line”.

usage: render.py SPEC.json
SPEC = {"out": "/path/out.png",
        "panels": [{"image": "/path/frame_01.png",
                    "lines": [{"speaker": "Ben", "text": "And maybe someone would want the old carpet."}]}]}
speaker may be "" (prints just the quoted line). Text is given without surrounding quotes.
env: FONTS_DIR (default /app/assets/fonts) — Inter-Bold.otf / Inter-SemiBold.otf, DejaVu as fallback.
"""
import json, os, sys
from PIL import Image, ImageDraw, ImageFont

FONTS = os.environ.get('FONTS_DIR', '/app/assets/fonts')
BOLD = [os.path.join(FONTS, 'Inter-Bold.otf'), '/usr/share/fonts/opentype/inter/Inter-Bold.otf',
        '/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf']
SEMI = [os.path.join(FONTS, 'Inter-SemiBold.otf'), '/usr/share/fonts/opentype/inter/Inter-SemiBold.otf',
        '/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf']


def font(paths, size):
    for p in paths:
        try:
            return ImageFont.truetype(p, size)
        except OSError:
            pass
    return ImageFont.load_default(size)


def tidy(t):
    t = t.strip().strip('"“”').strip()
    return t.replace("'", '’')


def runs_for(line, fb, fs):
    """A subtitle as a list of (word, font) tokens; the name prefix stays bold."""
    toks = []
    if line.get('speaker'):
        toks.append((line['speaker'].strip() + ':', fb))
    words = ('“' + tidy(line['text']) + '”').split()
    toks += [(w, fs) for w in words]
    return toks


def width(d, toks):
    sp = d.textlength(' ', font=toks[0][1]) if toks else 0
    return sum(d.textlength(t, font=f) for t, f in toks) + sp * (len(toks) - 1)


def wrap(d, toks, maxw):
    """One row if it fits, else the most balanced split into rows that fit."""
    if width(d, toks) <= maxw:
        return [toks]
    best = None
    for k in range(1, len(toks)):
        w = max(width(d, toks[:k]), width(d, toks[k:]))
        if best is None or w < best[0]:
            best = (w, k)
    if best and best[0] <= maxw:
        return [toks[:best[1]], toks[best[1]:]]
    rows, cur = [], []
    for t in toks:   # greedy fallback for very long lines
        if cur and width(d, cur + [t]) > maxw:
            rows.append(cur); cur = []
        cur.append(t)
    return rows + [cur]


def main():
    spec = json.load(open(sys.argv[1]))
    if not spec.get('panels'):
        raise RuntimeError('no panels to render')
    frames = [Image.open(p['image']).convert('RGB') for p in spec['panels']]
    W = max(f.width for f in frames)
    frames = [f if f.width == W else f.resize((W, round(f.height * W / f.width)), Image.LANCZOS) for f in frames]
    maxw = W - round(W * 0.045)
    probe = ImageDraw.Draw(frames[0])
    # one font size for the whole strip: largest from 3.2% to 2.6% of width where every subtitle fits on one row.
    # Lines too long to fit even at the minimum will wrap anyway, so they don't get to shrink everyone else.
    hi, lo = round(W * 0.032), round(W * 0.026)
    all_lines = [l for p in spec['panels'] for l in p['lines'] if l.get('text', '').strip()]
    fit_lines = [l for l in all_lines if width(probe, runs_for(l, font(BOLD, lo), font(SEMI, lo))) <= maxw]
    size = lo
    for s in range(hi, lo - 1, -1):
        fb, fs = font(BOLD, s), font(SEMI, s)
        if all(width(probe, runs_for(l, fb, fs)) <= maxw for l in fit_lines):
            size = s; break
    fb, fs = font(BOLD, size), font(SEMI, size)
    stroke, lineh = max(2, round(size * 0.095)), round(size * 1.33)
    for f, p in zip(frames, spec['panels']):
        d = ImageDraw.Draw(f)
        rows = [r for l in p['lines'] if l.get('text', '').strip() for r in wrap(d, runs_for(l, fb, fs), maxw)]
        y = f.height - round(f.height * 0.054) - len(rows) * lineh
        for r in rows:
            x = (W - width(d, r)) / 2
            sp = d.textlength(' ', font=fs)
            for t, fnt in r:
                d.text((x, y), t, font=fnt, fill=(255, 255, 255), stroke_width=stroke, stroke_fill=(0, 0, 0))
                x += d.textlength(t, font=fnt) + sp
            y += lineh
    M, G = round(W * 0.024), round(W * 0.021)
    page = Image.new('RGB', (W + 2 * M, 2 * M + sum(f.height for f in frames) + G * (len(frames) - 1)), (0, 0, 0))
    y = M
    for f in frames:
        page.paste(f, (M, y)); y += f.height + G
    page.save(spec['out'], optimize=True)
    prev = spec['out'].rsplit('.', 1)[0] + '_sm.jpg'           # small preview for the UI / MCP
    pw = 720
    page.resize((pw, round(page.height * pw / page.width)), Image.LANCZOS).save(prev, quality=85)
    print(spec['out'], page.size, f'font {size}px')


if __name__ == '__main__':
    try:
        main()
    except Exception as e:
        print(f'ERROR {e}', flush=True)
        sys.exit(1)
