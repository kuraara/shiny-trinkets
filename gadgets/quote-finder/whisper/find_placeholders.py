"""Find subtitle cues that hide foreign dialogue behind a placeholder instead of translating it
("[speaking Spanish]", "(SPEAKING FOREIGN LANGUAGE)", "[in Russian]") in Quote Finder's DB.

    docker cp quote-finder:/data/quotes.db{,-wal,-shm} .     # copy all three, then:
    python3 find_placeholders.py quotes.db placeholders.json

Reads the DB read-only. A cue counts when a foreign-speech bracket is all there is in it
("full"), or all there is on one speaker's line of a shared cue ("partial": "- Get out. -
[speaking French]"). Not counted: lines that already carry a translation ("[in Spanish] Get
down!"), accents, singing and music, and made-up languages (gibberish, alien, Klingon, Belter).
Output rows: media_id, title, kind, path, source, idx, start_ms, end_ms, text, full|partial.
"""
import collections, json, re, sqlite3, sys

LANGS = (r"non[- ]?english|foreign(?: language)?|spanish|french|german|italian|russian|korean|japanese|chinese|"
         r"mandarin|cantonese|arabic|hindi|urdu|portuguese|hebrew|yiddish|polish|dutch|swedish|norwegian|danish|"
         r"finnish|greek|turkish|vietnamese|thai|tagalog|filipino|persian|farsi|pashto|dari|swahili|latin|"
         r"hungarian|czech|romanian|serbian|croatian|ukrainian|indonesian|malay|punjabi|bengali|tamil|gaelic|"
         r"irish|welsh|klingon|elvish|navajo|creole|haitian|somali|amharic|zulu|xhosa|afrikaans|icelandic|"
         r"tibetan|mongolian|nepali|khmer|lao|burmese|albanian|bulgarian|slovak|catalan|basque|maori|hawaiian|"
         r"samoan|inuktitut|cherokee|apache|lakota|sioux|alien|native language|own language|another language|"
         r"other language|sign language|gibberish")
VERBS = (r"speak(?:s|ing)?|talk(?:s|ing)?|in|say(?:s|ing)?|chant(?:s|ing)?|sing(?:s|ing)?|whisper(?:s|ing)?|"
         r"shout(?:s|ing)?|yell(?:s|ing)?|mutter(?:s|ing)?|continues?(?: in)?|convers(?:es|ing|ation)|language|"
         r"dialogue|indistinct")
CANDIDATE = re.compile(rf"[\[(][^\])]{{0,60}}?\b(?:{VERBS})\b[^\])]{{0,40}}?\b(?:{LANGS})\b[^\])]{{0,40}}[\])]"
                       rf"|[\[(]\s*(?:{LANGS})\b[^\])]{{0,40}}[\])]", re.I)
BR = re.compile(r"[\[(][^\])]*[\])]")
FOREIGN = re.compile(r"non[- ]?english|foreign|spanish|french|german|italian|russian|korean|japanese|chinese|"
                     r"mandarin|cantonese|arabic|hindi|portuguese|indonesian|gaelic|hawaiian|creole|native language|"
                     r"other language|own language|another language|irish|swedish|norwegian", re.I)
ACCENT = re.compile(r"accent", re.I)
MUSIC = re.compile(r"sing|song|music|rap\b|rapping|♪|instrumental|folk|carol|operatic|chant", re.I)
INVENTED = re.compile(r"gibberish|alien|klingon|belter|elvish|sign language", re.I)
SPEECH = re.compile(r"speak|talk|\bin\b|say|shout|yell|mutter|whisper|continu|convers|chatter|pray|call|respond|"
                    r"thank|curse|announce|language|reading|\w+, in ", re.I)


def placeholder_bracket(b):
    return (FOREIGN.search(b) and SPEECH.search(b) and not ACCENT.search(b) and not MUSIC.search(b)
            and not INVENTED.search(b))


def leftover(s):
    s = BR.sub("", s)
    s = re.sub(r"\b[A-Z][A-Z .']+:", " ", s)  # "MAN:" speaker labels
    return s.strip(" -–—.,…:")


def segments(t):  # the speakers of a shared cue ("- Line one. - Line two.")
    return [s for s in re.split(r"(?:^|\s)-(?=\s*[\[(A-Z'\"¡¿])", t) if s.strip()]


def kind_of(text):
    bs = [b for b in BR.findall(text) if FOREIGN.search(b) or INVENTED.search(b)]
    if not bs or not any(placeholder_bracket(b) for b in bs):
        return None
    if not leftover(text):
        return "full"
    for s in segments(text):
        if not leftover(s) and any(placeholder_bracket(b) for b in BR.findall(s)):
            return "partial"
    return None


def main(db_path, out_path):
    db = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)
    rows = db.execute("SELECT m.id, m.title, m.kind, m.path, m.source, c.idx, c.start_ms, c.end_ms, c.text "
                      "FROM cues c JOIN media m ON m.id = c.media_id")
    out = []
    for r in rows:
        if CANDIDATE.search(r[8]) and (k := kind_of(r[8])):
            out.append(list(r) + [k])
    json.dump(out, open(out_path, "w"))
    titles = collections.Counter(r[1] for r in out)
    files = {r[3] for r in out}
    print(f"{len(out)} placeholder cues in {len(files)} files, {len(titles)} titles")
    for t, n in titles.most_common():
        print(f"{n:5d}  {t}")


if __name__ == "__main__":
    main(sys.argv[1], sys.argv[2])
