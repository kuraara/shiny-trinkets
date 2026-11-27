#!/usr/bin/env python3
"""Check the content pack inside round-timer.html.

    python3 tools/check_content.py round-timer.html

Reads the <script type="application/json" id="content-pack"> block and applies the same rules
the page applies when it loads (the page skips bad entries; this makes them fail loudly instead).
Exit status 0 = fine (warnings allowed), 1 = errors, 2 = could not read the pack.
Standard library only.
"""
import json
import re
import sys

DRAWN_PUNCHES = {"1", "2", "3", "4", "5", "6", "slip"}  # punch codes the page can draw


def load(path):
    html = open(path, encoding="utf-8").read()
    m = re.search(r'<script type="application/json" id="content-pack">(.*?)</script>', html, re.S)
    if not m:
        sys.exit("no <script type=\"application/json\" id=\"content-pack\"> block found")
    try:
        return json.loads(m.group(1))
    except json.JSONDecodeError as e:
        print(f"ERROR content-pack is not valid JSON: {e}")
        sys.exit(2)


def check(pack):
    errors, warnings = [], []
    err, warn = errors.append, warnings.append

    def s(v):
        return isinstance(v, str) and v.strip() != ""

    # punches
    codes = {}
    for i, p in enumerate(pack.get("punches", [])):
        code = p.get("code") if isinstance(p, dict) else None
        if not (isinstance(code, (int, str)) and not isinstance(code, bool)) or not s(p.get("label")):
            err(f"punches[{i}] needs a \"code\" and a \"label\"")
            continue
        if str(code) in codes:
            err(f"punches[{i}] repeats code {code}")
            continue
        codes[str(code)] = p
        if str(code) not in DRAWN_PUNCHES:
            warn(f"punches[{i}] code {code!r} has no drawing in the page, so it will show the guard pose only")
    easy = [p["easyOn"] for p in codes.values() if s(p.get("easyOn"))]
    if len(easy) != len(set(easy)):
        err("two punches share the same \"easyOn\" label")

    # combos
    seen = set()
    total = 0
    for gi, g in enumerate(pack.get("combos", [])):
        if not isinstance(g, dict) or not isinstance(g.get("seqs"), list):
            err(f"combos[{gi}] needs a \"seqs\" list")
            continue
        for si, seq in enumerate(g["seqs"]):
            where = f"combos[{gi}].seqs[{si}]"
            if not isinstance(seq, list) or not 1 <= len(seq) <= 8:
                err(f"{where} must be a list of 1 to 8 punch codes")
                continue
            unknown = [c for c in seq if str(c) not in codes]
            if unknown:
                err(f"{where} uses unknown punch code(s): {unknown}")
                continue
            key = json.dumps(seq)
            if key in seen:
                err(f"{where} duplicates an earlier combo")
                continue
            seen.add(key)
            total += 1
    if total == 0:
        err("no usable combos")
    for i, seq in enumerate(pack.get("defaultRounds", [])):
        if json.dumps(seq) not in seen:
            err(f"defaultRounds[{i}] {seq} is not one of the combos")

    # equipment
    eq = {}
    for i, e in enumerate(pack.get("equipment", [])):
        if not isinstance(e, dict) or not s(e.get("id")) or not s(e.get("label")):
            err(f"equipment[{i}] needs an \"id\" and a \"label\"")
            continue
        if e["id"] in eq:
            err(f"equipment[{i}] repeats id \"{e['id']}\"")
            continue
        if not isinstance(e.get("on"), bool):
            warn(f"equipment[{i}] (\"{e['id']}\") should say \"on\": true or false")
        eq[e["id"]] = e

    # warm-up
    for i, w in enumerate(pack.get("warmup", [])):
        if not isinstance(w, dict) or not s(w.get("name")) or not s(w.get("cue")):
            err(f"warmup[{i}] needs a \"name\" and a \"cue\"")

    # exercises
    ids, focus_seen = {}, []
    for i, x in enumerate(pack.get("exercises", [])):
        tag = f"exercises[{i}]" + (f" (\"{x.get('id')}\")" if isinstance(x, dict) and x.get("id") else "")
        if not isinstance(x, dict) or not all(s(x.get(k)) for k in ("id", "name", "focus", "cue")):
            err(f"{tag} needs \"id\", \"name\", \"focus\" and \"cue\"")
            continue
        if x["id"] in ids:
            err(f"{tag} repeats an earlier id")
            continue
        ids[x["id"]] = x
        if x["focus"] not in focus_seen:
            focus_seen.append(x["focus"])
        valid_needs = 0
        for ni, n in enumerate(x.get("needs", []) or []):
            if not isinstance(n, dict) or not s(n.get("eq")) or not s(n.get("with")):
                err(f"{tag}.needs[{ni}] needs \"eq\" and \"with\"")
            elif n["eq"] not in eq:
                err(f"{tag}.needs[{ni}] uses unknown equipment \"{n['eq']}\"")
            else:
                valid_needs += 1
        if x.get("needs") and not valid_needs and x.get("bodyweightOk") is not True:
            err(f"{tag} can never be done (no valid equipment)")
        if not s(x.get("why")):
            warn(f"{tag} has no \"why\" line")
        if len(x["cue"]) > 140:
            warn(f"{tag} cue is long ({len(x['cue'])} characters); it has to fit on a phone screen")

    for i, d in enumerate(pack.get("defaultExercises", [])):
        if d not in ids:
            err(f"defaultExercises[{i}] (\"{d}\") is not an exercise id")
    # workouts: whole sessions (one combo per round, optional exercises)
    wk_ids = set()
    for i, w in enumerate(pack.get("workouts", [])):
        tag = f"workouts[{i}]" + (f" (\"{w.get('id')}\")" if isinstance(w, dict) and w.get("id") else "")
        if not isinstance(w, dict) or not s(w.get("id")) or not s(w.get("name")) or not isinstance(w.get("rounds"), list):
            err(f"{tag} needs \"id\", \"name\" and a \"rounds\" list")
            continue
        if w["id"] in wk_ids:
            err(f"{tag} repeats an earlier id")
            continue
        wk_ids.add(w["id"])
        if not 1 <= len(w["rounds"]) <= 30:
            err(f"{tag} needs 1 to 30 rounds")
        for r, seq in enumerate(w["rounds"]):
            if json.dumps(seq) not in seen:
                err(f"{tag}.rounds[{r}] {seq} is not one of the combos")
        for e, ex_id in enumerate(w.get("exercises", []) or []):
            if ex_id not in ids:
                err(f"{tag}.exercises[{e}] (\"{ex_id}\") is not an exercise id")
        if not s(w.get("note")):
            warn(f"{tag} has no \"note\" (one line on what it trains)")
        elif len(w["note"]) > 140:
            warn(f"{tag} note is long ({len(w['note'])} characters)")
        if w.get("source") and not (isinstance(w["source"], str) and w["source"].startswith("https://")):
            warn(f"{tag} source should be an https:// link, otherwise the page ignores it")
    for i, w in enumerate(pack.get("workouts", [])):
        if isinstance(w, dict) and isinstance(w.get("rounds"), list):
            same = [o["id"] for o in pack["workouts"][:i]
                    if isinstance(o, dict) and o.get("rounds") == w["rounds"] and o.get("id") != w.get("id")]
            if same:
                warn(f"workouts[{i}] has the same rounds as \"{same[0]}\"")

    for f in pack.get("focusOrder", []):
        if f not in focus_seen:
            warn(f"focusOrder lists \"{f}\" but no exercise has that focus")

    # is there enough to do with no equipment at all?
    bodyweight = [k for k, x in ids.items() if x.get("bodyweightOk") is True or not x.get("needs")]
    if len(bodyweight) < 8:
        warn(f"only {len(bodyweight)} exercises work with no equipment (aim for 8 or more)")

    stats = {
        "combos": total, "exercises": len(ids), "bodyweight_only": len(bodyweight),
        "warmup_moves": len(pack.get("warmup", [])), "focus": focus_seen,
        "workouts": len(wk_ids),
    }
    return errors, warnings, stats


def main():
    path = sys.argv[1] if len(sys.argv) > 1 else "round-timer.html"
    errors, warnings, stats = check(load(path))
    for w in warnings:
        print("warning:", w)
    for e in errors:
        print("ERROR:  ", e)
    print(f"{stats['combos']} combos, {stats['exercises']} exercises "
          f"({stats['bodyweight_only']} need no equipment), {stats['warmup_moves']} warm-up moves, "
          f"{stats['workouts']} workouts; "
          f"focus groups: {', '.join(stats['focus'])}")
    print("FAILED" if errors else "content pack OK")
    sys.exit(1 if errors else 0)


if __name__ == "__main__":
    main()
