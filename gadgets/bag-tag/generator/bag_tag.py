#!/usr/bin/env python3
"""Bag tag generator: name + font in, print-ready base and inlay STLs out.

Wraps bag_tag.scad. OpenSCAD does all the 2D work (text, offsets, outlines) and measures the
name itself (part="probe"), so any font OpenSCAD can see works and sizes are exact. The text
size is picked so the capitals are `--letter-h` tall but the name is no longer than
`--max-len`. The 3D parts are then built from OpenSCAD's 2D sections with manifold3d
(about a second, against minutes for a CGAL render in OpenSCAD 2021). Also writes a top-view
SVG preview with an AAA battery for scale, and an info JSON.

    python3 bag_tag.py --name AUBREY                       # defaults: Sniglet ExtraBold, 12 mm caps
    python3 bag_tag.py --name "Zoë" --font "Titan One" --png
    python3 bag_tag.py --name Maximilian --max-len 60 --set clearance=0 --out out/
    python3 bag_tag.py --list-fonts
    python3 bag_tag.py --name Mia --preview-only --json    # 2D preview + numbers only (< 1 s)

Needs: python3, OpenSCAD 2021.01+ (env OPENSCAD overrides the binary), numpy + manifold3d
(pip; without them OpenSCAD renders the 3D itself, slowly), shapely (checks; optional),
xvfb-run (only for --png). Exit code 0 = files written, 2 = bad input or failure (message on
stderr, or {"error": ...} on stdout with --json). This script is also the reference for the
shiny-trinkets "bag-tag" gadget.
"""
import argparse, json, os, re, shutil, struct, subprocess, sys, tempfile, time, pathlib

HERE = pathlib.Path(__file__).resolve().parent
SCAD = HERE / "bag_tag.scad"
FONT_DIR = HERE / "fonts"
OPENSCAD = os.environ.get("OPENSCAD", "openscad")
PROBE_SIZE = 10.0
DEFAULT_FONT = "Sniglet:style=ExtraBold"
AAA = (10.5, 44.5)           # diameter, length (mm): drawn in the preview for scale
MAX_NAME = 24                # characters
NUM = r"-?\d+(?:\.\d+)?(?:[eE][-+]?\d+)?"


# ------------------------------------------------------------------ OpenSCAD plumbing

def scad_params():
    """Customizer parameters in bag_tag.scad: {name: {"default", "help", "group"}}."""
    out, group, helps = {}, "", []
    for line in SCAD.read_text().splitlines():
        if line.startswith("$fn"):
            break
        g = re.match(r"^/\* \[(.+)\] \*/", line)
        if g:
            group, helps = g.group(1), []
            continue
        if line.startswith("//"):
            helps.append(line[2:].strip())
            continue
        m = re.match(r"^([a-z_][a-z0-9_]*)\s*=\s*([^;]+);\s*(?://\s*(\[.*\]))?", line)
        if m and group and m.group(1) not in ("part", "slab_set", "slab", "ink"):
            raw = m.group(2).strip()
            val = raw[1:-1] if raw.startswith('"') else (raw == "true" if raw in ("true", "false") else float(raw))
            p = {"default": val, "group": group, "help": " ".join(helps)}
            if m.group(3):
                p["choices"] = [c.strip() for c in m.group(3)[1:-1].split(",")]
            out[m.group(1)] = p
        helps = [] if not line.startswith("//") else helps
    return out


def scad_value(v):
    if isinstance(v, bool):
        return "true" if v else "false"
    if isinstance(v, (int, float)):
        return repr(round(float(v), 4))
    if isinstance(v, (list, tuple)):
        return "[" + ",".join(scad_value(x) for x in v) + "]"
    s = str(v)
    if any(ord(c) < 32 for c in s):
        raise ValueError("control characters are not allowed")
    return '"' + s.replace("\\", "\\\\").replace('"', '\\"') + '"'


def parse_value(s):
    """--set key=value: number, true/false, or string."""
    if s in ("true", "false"):
        return s == "true"
    try:
        return float(s)
    except ValueError:
        return s


def openscad(out, params, part, extra=None, timeout=900):
    cmd = [OPENSCAD, "-o", str(out)]
    for k, v in {**params, "part": part, **(extra or {})}.items():
        cmd += ["-D", f"{k}={scad_value(v)}"]
    cmd.append(str(SCAD))
    r = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout)
    if r.returncode != 0 or not pathlib.Path(out).exists():
        raise RuntimeError(f"openscad {part} failed:\n{r.stderr[-1500:]}")
    if "Can't get font" in r.stderr or "Font not found" in r.stderr:
        raise RuntimeError(f"font {params.get('font')!r} not found (see --list-fonts)")
    return r.stderr


def scad_info(params, work):
    err = openscad(work / "info.echo", params, "info")
    m = re.search(r'ECHO: "INFO (.*)"', (work / "info.echo").read_text() + err)
    if not m:
        raise RuntimeError("no INFO from bag_tag.scad:\n" + err[-800:])
    return json.loads(m.group(1))


def svg_rings(path):
    """Closed rings from an OpenSCAD 2D SVG export, in model coordinates (SVG y is flipped)."""
    txt = pathlib.Path(path).read_text()
    rings = []
    for d in re.findall(r'<path d="([^"]*)"', txt):
        for chunk in d.split("M"):
            pts = [(float(x), -float(y)) for x, y in re.findall(rf"({NUM}),({NUM})", chunk)]
            if len(pts) >= 3:
                rings.append(pts)
    return rings


def section(params, work, part, extra=None, tag=""):
    p = work / f"{part}{tag}.svg"
    openscad(p, params, part, extra)
    return svg_rings(p)


def bbox(rings):
    xs = [x for r in rings for x, _ in r]
    ys = [y for r in rings for _, y in r]
    return (min(xs), max(xs), min(ys), max(ys)) if xs else None


# ------------------------------------------------------------------ STL

def write_stl(path, verts, tris):
    """Binary STL from vertex and triangle arrays. Returns [min, max] bounds."""
    import numpy as np
    v = np.asarray(verts, dtype=np.float64)[:, :3]
    t = np.asarray(tris, dtype=np.int64)
    a, b, c = v[t[:, 0]], v[t[:, 1]], v[t[:, 2]]
    n = np.cross(b - a, c - a)
    n /= np.maximum(np.linalg.norm(n, axis=1, keepdims=True), 1e-12)
    rec = np.zeros(len(t), dtype=[("n", "<f4", 3), ("v", "<f4", (3, 3)), ("a", "<u2")])
    rec["n"], rec["v"] = n, np.stack([a, b, c], axis=1)
    with open(path, "wb") as f:
        f.write(b"bag_tag.py".ljust(80, b" ") + struct.pack("<I", len(t)))
        f.write(rec.tobytes())
    return [v.min(axis=0).tolist(), v.max(axis=0).tolist()]


def stl_ascii_to_binary(path):
    """OpenSCAD 2021 writes ASCII STL; rewrite as binary (5x smaller)."""
    data = pathlib.Path(path).read_bytes()
    if data[:5] != b"solid":
        return None
    verts = [tuple(map(float, v)) for v in re.findall(rb"vertex\s+(\S+)\s+(\S+)\s+(\S+)", data)]
    return write_stl(path, verts, [(3 * i, 3 * i + 1, 3 * i + 2) for i in range(len(verts) // 3)])


def build_3d(params, work, info, out_base, out_inlay):
    """Extrude OpenSCAD's slab sections with manifold3d. Falls back to an OpenSCAD render."""
    try:
        import numpy as np
        import manifold3d as mf
    except ImportError:
        for part, out in (("base", out_base), ("inlay", out_inlay)):
            openscad(out, params, part)
            stl_ascii_to_binary(out)
        return "openscad"

    def stack(slab_set, slabs):
        solid = None
        for k, s in enumerate(slabs):
            z0, z1 = s[0], s[1]
            if z1 <= z0 + 1e-6:
                continue
            rings = section(params, work, "slab2d", {"slab_set": slab_set, "slab": k}, f"_{slab_set}{k}")
            if not rings:
                continue
            cs = mf.CrossSection([np.array(r, dtype=np.float64) for r in rings], mf.FillRule.EvenOdd)
            prism = cs.extrude(z1 - z0).translate((0, 0, z0))
            solid = prism if solid is None else solid + prism
        if solid is None or solid.status() != mf.Error.NoError:
            raise RuntimeError(f"{slab_set}: could not build a solid")
        return solid

    base = stack("base", info["base_slabs"])
    inlay = stack("inlay", info["inlay_slabs"])
    if info["inlay_flip"]:                       # face-down: a rotation, never a mirror
        inlay = inlay.rotate((0, 180, 0)).translate((0, 0, info["inlay_top"]))
    else:
        inlay = inlay.translate((0, 0, -info["floor_t"]))
    for solid, out in ((base, out_base), (inlay, out_inlay)):
        m = solid.to_mesh()
        write_stl(out, m.vert_properties, m.tri_verts)
    return "manifold"


# ------------------------------------------------------------------ fonts

def list_fonts():
    """Bundled fonts first, then whatever fontconfig (and so OpenSCAD) can see."""
    fmt = "%{family[0]}\t%{style[0]}\t%{file}\n"

    def scan(cmd):
        try:
            r = subprocess.run([*cmd, "--format", fmt], capture_output=True, text=True, timeout=30)
            return [l.split("\t") for l in r.stdout.splitlines() if l.count("\t") == 2]
        except (OSError, subprocess.TimeoutExpired):
            return []
    fonts, seen = [], set()
    for f in sorted(FONT_DIR.glob("*.ttf")):
        for fam, style, file in scan(["fc-scan", str(f)]):
            name = f"{fam}:style={style}"
            if name not in seen:
                seen.add(name)
                fonts.append({"font": name, "family": fam, "style": style, "bundled": True, "file": file})
    for fam, style, file in sorted(scan(["fc-list", ":"])):
        name = f"{fam}:style={style}"
        if name not in seen and file.lower().endswith((".ttf", ".otf")):
            seen.add(name)
            fonts.append({"font": name, "family": fam, "style": style, "bundled": False, "file": file})
    return fonts


def resolve_font(font):
    """"Titan One" or "Titan One:style=Regular" → the font entry. OpenSCAD silently swaps an
    unknown font for a default one, so unknown names are an error here."""
    fam, _, rest = font.partition(":")
    style = rest.split("style=", 1)[1] if "style=" in rest else ""
    fonts = list_fonts()
    same = [f for f in fonts if f["family"].lower() == fam.strip().lower()]
    if not same:
        raise ValueError(f"font {fam!r} not found (see --list-fonts)")
    if style:
        exact = [f for f in same if f["style"].lower() == style.lower()]
        if not exact:
            raise ValueError(f"{fam!r} has no style {style!r}; it has: "
                             + ", ".join(f["style"] for f in same))
        return exact[0]
    regular = [f for f in same if f["style"].lower() in ("regular", "book", "normal")]
    return (regular or same)[0]


def missing_glyphs(font_file, text):
    """Characters the font can't draw (OpenSCAD would print an empty box). Needs fontTools."""
    try:
        from fontTools.ttLib import TTFont
    except ImportError:
        return []
    try:
        cmap = TTFont(font_file, fontNumber=0, lazy=True).getBestCmap() or {}
    except Exception:
        return []
    return sorted({c for c in text if not c.isspace() and ord(c) not in cmap})


# ------------------------------------------------------------------ the generator

def measure(params, work, size):
    rings = section({**params, "text_size": size}, work, "probe")
    name_r = [r for r in rings if max(y for _, y in r) < 500]
    h_r = [r for r in rings if min(y for _, y in r) > 500]
    nb, hb = bbox(name_r), bbox(h_r)
    if nb is None:
        raise RuntimeError(f"font {params['font']!r} drew nothing for {params['name']!r} "
                           "(unknown font, or no glyphs for these characters)")
    cap = (hb[3] - hb[2]) if hb else (nb[3] - nb[2])
    return nb, cap


def generate(args):
    t0 = time.time()
    name = " ".join(args.name.split())
    if not name or len(name) > MAX_NAME:
        raise ValueError(f"name must be 1–{MAX_NAME} characters")
    known = scad_params()
    font = resolve_font(args.font)
    missing = missing_glyphs(font["file"], name)
    if missing:
        raise ValueError(f"{font['family']} has no letters for: {' '.join(missing)}")
    params = {"name": name, "font": font["font"], "text_dir": args.dir}
    for kv in args.set or []:
        k, _, v = kv.partition("=")
        if k in ("name", "font", "text_dir", "text_size"):
            raise ValueError(f"use --{k.replace('_', '-').replace('text-dir', 'dir')} for {k}, not --set")
        if k not in known:
            raise ValueError(f"unknown parameter {k!r}; known: {', '.join(sorted(known))}")
        params[k] = parse_value(v)
    warnings = []
    out = pathlib.Path(args.out)
    out.mkdir(parents=True, exist_ok=True)
    stem = args.stem or re.sub(r"[^a-z0-9]+", "-", name.lower()).strip("-") or "tag"

    with tempfile.TemporaryDirectory() as tmp:
        work = pathlib.Path(tmp)
        # 1. size: capitals letter_h tall, then shrink to max_len
        nb, cap = measure(params, work, PROBE_SIZE)
        if args.text_size:
            size = args.text_size
        else:
            size = PROBE_SIZE * args.letter_h / cap
            length = (nb[1] - nb[0]) * size / PROBE_SIZE
            if args.max_len and length > args.max_len:
                size *= args.max_len / length
                warnings.append(f"long name: letters shrunk to {cap * size / PROBE_SIZE:.1f} mm "
                                f"(from {args.letter_h:g}) to keep it within {args.max_len:g} mm")
        nb, cap = measure(params, work, size)       # exact ink at the final size
        params["text_size"] = round(size, 4)
        params["ink"] = [round(v, 4) for v in nb]
        if cap < 7:
            warnings.append(f"letters are only {cap:.1f} mm tall: thin strokes may not print "
                            "well as an inlay; use a heavier font or a shorter name")

        # 2. 2D sections + checks (shapely optional)
        sec = {p: section(params, work, p) for p in ("outline2d", "pocket2d", "inlay2d")}
        sec["sprue2d"] = section(params, work, "sprue2d") if params.get("sprue", True) else []
        checks = check_2d(sec, params, warnings)
        if checks.get("outline_holes") and params.get("outline_style", "follow") == "follow":
            params["outline_style"] = "bar"
            warnings.append("the rim left holes inside big letters; switched to outline_style=bar")
            sec["outline2d"] = section(params, work, "outline2d", tag="_bar")
            checks = check_2d(sec, params, [])
        info_scad = scad_info(params, work)

        ob = bbox(sec["outline2d"])
        files = {}
        info = {
            "name": name, "font": params["font"], "text_dir": args.dir,
            "letter_h": round(cap, 2), "name_len": round(nb[1] - nb[0], 2),
            "length": round(ob[3] - ob[2], 1), "width": round(ob[1] - ob[0], 1),
            "thickness": info_scad["thickness"], "floor_t": info_scad["floor_t"],
            "inlay_h": round(info_scad["inlay_top"] - info_scad["inlay_bot"], 2),
            "inlay_face_down": info_scad["inlay_flip"],
            "params": {k: v for k, v in params.items() if k not in ("name", "font")},
            **checks, "warnings": warnings, "files": files,
        }
        svg = out / f"{stem}_preview.svg"
        write_preview_svg(svg, sec, info)
        files["preview_svg"] = svg.name

        # 3. 3D
        if not args.preview_only:
            base_stl, inlay_stl = out / f"{stem}_base.stl", out / f"{stem}_inlay.stl"
            info["engine"] = build_3d(params, work, info_scad, base_stl, inlay_stl)
            files["base_stl"], files["inlay_stl"] = base_stl.name, inlay_stl.name
            if args.png:
                png = out / f"{stem}_preview.png"
                render_png(base_stl, inlay_stl, png, info_scad)
                files["preview_png"] = png.name
        info["seconds"] = round(time.time() - t0, 1)
        files["info_json"] = f"{stem}_info.json"
        (out / files["info_json"]).write_text(json.dumps(info, indent=1, ensure_ascii=False))
        return info


def check_2d(sec, params, warnings):
    try:
        from shapely.geometry import Polygon, MultiPolygon
    except ImportError:
        return {}

    def poly(rings):
        g = Polygon()
        for r in rings:
            g = g.symmetric_difference(Polygon(r).buffer(0))
        return g

    def parts(g):
        return [p for p in (g.geoms if isinstance(g, MultiPolygon) else [g]) if not p.is_empty]

    outline, inlay = poly(sec["outline2d"]), poly(sec["inlay2d"])
    pocket, sprue = poly(sec["pocket2d"]), poly(sec["sprue2d"])
    loop_holes = 0 if params.get("loop") is False else 1
    holes = sum(len(p.interiors) for p in parts(outline)) - loop_holes
    pieces = parts(inlay.union(sprue))
    # thin strokes: what two 0.4 mm perimeters can't fill
    thin = inlay.area - inlay.buffer(-0.4).buffer(0.4).area if inlay.area else 0
    if inlay.area and thin / inlay.area > 0.06:
        warnings.append(f"{100 * thin / inlay.area:.0f}% of the inlay is narrower than 0.8 mm: "
                        "bigger letters or a heavier font will print cleaner")
    islands = [Polygon(i) for p in parts(pocket) for i in p.interiors]
    tiny = [i for i in islands if i.buffer(-0.5).is_empty]
    if tiny:
        warnings.append(f"{len(tiny)} base-colour dot(s) inside letters are under 1 mm wide and may "
                        "not print; raising weld (e.g. --set weld=0.65) fills them in")
    floating = [i for i in islands if i.difference(sprue).area < 0.05 * i.area]
    if floating:
        warnings.append(f"{len(floating)} base-colour island(s) inside letters sit right over the "
                        "sprue groove and would print in mid-air: try --set sprue_lift=… or sprue=false")
    if len(pieces) > (2 if params.get("flower") is not False else 1):
        warnings.append(f"the inlay is {len(pieces)} separate pieces (accents, dots, or sprue off)")
    return {"outline_holes": max(holes, 0), "inlay_pieces": len(pieces),
            "inlay_area": round(inlay.area, 1), "islands": len(islands), "floating_islands": len(floating)}


def write_preview_svg(path, sec, info):
    """Top view: base colour, inlay colour, an AAA battery alongside for scale."""
    outline, pocket = sec["outline2d"], sec["pocket2d"]
    x0, x1, y0, y1 = bbox(outline)
    bw, bl = AAA
    bx0, pad = x0 - 8 - bw, 4
    vx0, vx1 = bx0 - pad, x1 + pad
    vy0, vy1 = min(y0, y1 - bl) - pad - 8, y1 + pad      # model coords; SVG y = -model y

    def d(rings):
        return " ".join("M" + " L".join(f"{x:.2f},{-y:.2f}" for x, y in r) + "Z" for r in rings)

    w, h = vx1 - vx0, vy1 - vy0
    cx, cy = bx0 + bw / 2, -y1 + bl / 2
    path.write_text(f'''<svg xmlns="http://www.w3.org/2000/svg" viewBox="{vx0:.2f} {-vy1:.2f} {w:.2f} {h:.2f}" width="{w * 4:.0f}" height="{h * 4:.0f}">
<title>{escape(info["name"])} bag tag</title>
<path d="{d(outline)}" fill="#f5d000" stroke="#b39500" stroke-width="0.25" fill-rule="evenodd"/>
<path d="{d(pocket)}" fill="#fbfaf2" stroke="#d8d2b0" stroke-width="0.2" fill-rule="evenodd"/>
<rect x="{bx0:.2f}" y="{-y1:.2f}" width="{bw}" height="{bl}" rx="1.5" fill="#5a2a4a"/>
<rect x="{cx - 1.8:.2f}" y="{-y1 + bl:.2f}" width="3.6" height="0.8" fill="#aaa"/>
<text x="{cx:.2f}" y="{cy:.2f}" font-family="sans-serif" font-size="4" font-weight="bold" fill="#fff" text-anchor="middle" dominant-baseline="middle" transform="rotate(90 {cx:.2f} {cy:.2f})">AAA</text>
<text x="{(vx0 + vx1) / 2:.2f}" y="{-vy0 - 3:.2f}" font-family="sans-serif" font-size="3.6" fill="#555" text-anchor="middle">{info["length"]:g} × {info["width"]:g} × {info["thickness"]:g} mm</text>
</svg>
''')


def escape(s):
    return s.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")


def render_png(base, inlay, png, info_scad):
    """3D preview: the inlay put back in its pocket (undoing the face-down flip)."""
    place = (f"translate([0,0,{info_scad['inlay_top']}]) rotate([0,180,0])" if info_scad["inlay_flip"]
             else f"translate([0,0,{info_scad['floor_t']}])")
    view = png.with_suffix(".view.scad")
    view.write_text(f'color("#f5d000") import("{base.resolve()}");\n'
                    f'color("#fbfaf2") {place} import("{inlay.resolve()}");\n')
    xvfb = ["xvfb-run", "-a"] if shutil.which("xvfb-run") and not os.environ.get("DISPLAY") else []
    r = subprocess.run([*xvfb, OPENSCAD, "-o", str(png), "--imgsize=900,1200", "--colorscheme=Tomorrow",
                        "--camera=0,0,0,25,0,15,0", "--viewall", "--autocenter", "--projection=p",
                        str(view)], capture_output=True, text=True, timeout=300)
    view.unlink()
    if r.returncode or not png.exists():
        raise RuntimeError("PNG render failed:\n" + r.stderr[-800:])


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0],
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--name", help=f"text on the tag (1–{MAX_NAME} characters)")
    ap.add_argument("--font", default=DEFAULT_FONT, help=f"default {DEFAULT_FONT!r}; see --list-fonts")
    ap.add_argument("--letter-h", type=float, default=12.0, help="capital height, mm (default 12)")
    ap.add_argument("--max-len", type=float, default=62.0,
                    help="longest the name may be, mm; longer names get smaller letters (default 62, 0 = no limit)")
    ap.add_argument("--text-size", type=float, help="raw OpenSCAD text size; overrides --letter-h/--max-len")
    ap.add_argument("--dir", choices=["down", "up"], default="down", help="reading direction (default down)")
    ap.add_argument("--set", action="append", metavar="KEY=VALUE",
                    help="any other bag_tag.scad parameter, e.g. clearance=0, flower=false, sprue=false")
    ap.add_argument("--out", default="out", help="output folder (default ./out)")
    ap.add_argument("--stem", help="file name stem (default: the name, lowercased)")
    ap.add_argument("--png", action="store_true", help="also render a 3D preview PNG (needs xvfb-run)")
    ap.add_argument("--preview-only", action="store_true", help="2D preview SVG + info only, no STLs")
    ap.add_argument("--json", action="store_true", help="print the info JSON to stdout")
    ap.add_argument("--list-fonts", action="store_true", help="print fonts as JSON and exit")
    ap.add_argument("--params", action="store_true", help="print bag_tag.scad parameters as JSON and exit")
    args = ap.parse_args()
    if args.list_fonts:
        print(json.dumps(list_fonts(), indent=1, ensure_ascii=False))
        return
    if args.params:
        print(json.dumps(scad_params(), indent=1))
        return
    if not args.name:
        ap.error("--name is required")
    try:
        info = generate(args)
    except (ValueError, RuntimeError, subprocess.TimeoutExpired) as e:
        if args.json:
            print(json.dumps({"error": str(e)}))
        else:
            print(f"error: {e}", file=sys.stderr)
        sys.exit(2)
    if args.json:
        print(json.dumps(info, indent=1, ensure_ascii=False))
    else:
        print(f"{info['name']}: {info['length']} × {info['width']} × {info['thickness']} mm, "
              f"letters {info['letter_h']} mm, inlay pieces {info.get('inlay_pieces', '?')}, "
              f"{info['seconds']} s → {args.out}/")
        for w in info["warnings"]:
            print("  warning:", w)


if __name__ == "__main__":
    main()
