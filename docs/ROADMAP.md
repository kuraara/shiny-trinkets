# Roadmap: "I want to…" landing (C) → Studio app (A) → scene workspace (B) → sentence search (D)

Written 2026-10-25. Mock-ups: https://claude.ai/artifact/8tMCA9M7WnjVRFt8iNTZS9
Repo: kuraara/shiny-trinkets (this one; commit to master).
Rules: no dev/hosting on the Pi; build on the build box; deploy with `tools/trinket deploy <id>`.
Test every phase in headless Chromium at 1280 px and 390 px before committing.

Status key: [ ] todo · [~] in progress · [x] done

## Phase 0 — where things are

See [ARCHITECTURE.md](ARCHITECTURE.md): gadgets are folders with a `gadget.json`; the registry
is assembled from them; `tools/trinket` lists, checks, creates and deploys.

## Phase 1 — C: "I want to…" view in the registry  [ ]

Goal: the registry home page starts from what you want to do, as in mock-up C.

1. [x] Intents: each gadget's `gadget.json` has an `intents` list (`phrase`, `desc`, `group`,
   optional `page`/`path`); `tools/trinket assemble` collects them into `registry.json`
   (`intents`, each pointing at the app or page it opens). Groups and their headings are in
   `registry/site.json`. Seven so far, including AirPrint to PDF ("…turn something on my phone
   into a PDF").
2. [ ] `home.js` / `index.html` / `home.css`: third view **I want to…** in the view switch
   (I want to… · Cards · Tiles). Full-width cards: icon 72 px, phrase (27 px bold), desc, a dark
   "<App name> →" button; grouped by `group` with small caps headings; at the bottom "Looking for
   Plex, Home Assistant…? See everything →" which switches to Cards. Phone layout as mock-up C
   (icon + phrase row, desc, button). Status dot of the app shown subtly (or greyed card + "down").
3. [ ] Default view = I want to… for new visitors; a saved choice still wins.
4. [ ] The filter box filters intents (phrase, desc, keywords, app name) in that view.
5. [ ] Intent links go to the app URL + optional `path` (so later intents can deep-link into
   Studio, e.g. `/?make=clip`).
6. [ ] README section; e2e screenshots (1280/390, light + dark); commit; `tools/trinket deploy registry`.

Done when: the home page opens on "I want to…", every gadget is reachable from a sentence, the
other two views still work, nothing else on the PC/Pi changed.

## Phase 2 — A: grow Quote Finder into "Studio"  [ ]

Goal: one app (mock-ups A + A-phone): side menu on desktop, bottom tabs (Find · Make · My stuff)
on phones, everything you make listed in one place. Built inside quote-finder (same container,
port 8104, Go + plain JS); standalone gadgets keep working.

2.1 [ ] **App shell.** Shared header/menu used by every Studio page: desktop side menu
   (Find: A quote · Make: Panels, A clip, One video from many, One picture from many · Share:
   Party photo wall (link out) · All gadgets →); phone bottom tabs Find / Make / My stuff with a
   badge. Pages: `/` (Find), `/make`, `/stuff`, existing `/panel.html`. Registry navbar still on top
   (or switched to `fab` mode for Studio so it doesn't double up — decide by screenshot).
2.2 [ ] **My stuff.** Server endpoint listing video-tools jobs (`GET /api/vt/jobs` proxy →
   video-tools `/api/jobs`), newest first: panels (thumbnail from the job's frame / rendered PNG,
   "Edit" → panel editor, "Download"), clips (thumbnail via first frame, progress bar while
   running, Download / Share), joins. Tray on desktop (right column, as mock-up A), page on phone.
   Note jobs are kept 24 h (JOB_TTL_HOURS) — say so; consider raising to 72 h.
2.3 [ ] **Make page.** Entry points that don't need a quote: Panels from your own video
   (upload → video-tools `/api/panel` without lines = speech recognition → panel editor),
   A clip from your own video (upload + start/end → `/api/clip`), One video from many (→ reel),
   One picture from many (Photo Stitch: link out first; later serve it under Studio).
   Uploads stream through a Quote Finder proxy to video-tools `/api/files` (no buffering; size cap).
2.4 [ ] **Reel (join).** "+ Add to a reel" on a quote selection (and on My stuff clips) keeps a
   list in localStorage; Reel view: reorder, remove, transition (none/fade/crossfade), "Join into
   one video". Server: for each scene make an accurate clip (existing scene → `/api/clip` flow),
   wait for them, then video-tools `/api/join`. Needs video-tools to accept a finished job output
   as a join input (add `job:<id>/<file>` inputs, or a `keep_as_file` option on clip jobs that
   registers the output as an upload) — design this first, keep inputs cleaned up.
2.5 [ ] **Registry.** New "Studio" gadget entry (or rename Quote Finder's entry), intents
   deep-link into Studio (`/make?what=clip` etc.); keep Panel Transcript / Join / Cut entries for now.
2.6 [ ] MCP: add `list_my_stuff`, `add_to_reel`/`join_scenes` tools to Quote Finder's MCP.
2.7 [ ] Tests: Go tests for new endpoints (fake video-tools as in panels_test.go); e2e flows on
   phone + desktop: find → panels → My stuff; find → clip → My stuff; two scenes → reel → join;
   upload → clip. README. Commit per sub-step.

Done when: from one app on a phone you can find a quote, make panels or a clip, join two scenes,
and find all of it again under My stuff.

## Phase 3 — B: scene workspace (later)  [ ]

Timeline editing for a picked scene (mock-up B): filmstrip thumbnails for the range (new
video-tools endpoint: N thumbnails of a clip), drag handles to trim to the frame, play the scene
in the page (stream the copied scene), "Make from this scene: Panels · Clip · GIF · Stills"
(GIF and stills are new video-tools jobs). Reel strip along the bottom. Plan in detail when
Phase 2 is done.

## Phase 4 — D: "I want to ___" as a sentence (later)  [ ]

Typed sentence matched against intents + keywords; suggestions can chain steps
("…make a clip of a line from a show" → Studio Find then Clip). Builds on Phase 1's intents and
Phase 2's deep links. Plan in detail after Phase 2.

## Open questions to raise with the user when reached

- Studio name ("Studio"? keep "Quote Finder"?) — default: keep the Quote Finder gadget, call the
  shell "Studio" in the header; easy to rename.
- Job retention (24 h now) — longer for My stuff?
- Retire the separate Panel Transcript / Join / Cut gadget entries once Studio covers them?
