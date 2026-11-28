# Round Timer: notes for agents that edit it

This folder is one self-contained page, `round-timer.html`, plus what surrounds it. The page
runs on its own (no server, no network) and is also served unchanged by the container here.
Keep both true.

## The one rule

**Content is data; code is code.** The combos, exercises, warm-up moves and equipment live in one
JSON block inside the page:

    <script type="application/json" id="content-pack"> … </script>

To add, remove or reword any of them, edit **only that block**. You don't need to read or change
the JavaScript below it. The page checks the block when it loads and skips any bad entry (it
says so in an amber note on the setup screen and in the console), so a slip can't break the timer.
A skipped entry is still a mistake, so check your work:

    python3 tools/check_content.py round-timer.html

Exit 0 = fine (warnings are advice). Exit 1 = errors, i.e. entries the page would skip. Python
standard library only. Then open the page: the setup screen should show no amber note.

If you touch anything outside the content block, also run `go test ./...` and `node test/e2e.js`
(see README; the e2e test clears the shared session, so point it at a scratch server).

## The session is saved (so keep it that way)

The page saves the running session so a reload or app switch lands back in it, and a second device
can follow it (README, "Switching apps"). Content edits never touch this. If you change the
**code**: every place that changes the session (a phase starting, pause, resume, shuffle, finish,
back to setup) calls `saveSoon()`; what a snapshot holds is built by `snapshot()` and read back by
`restoreSession()`, and `v` must change if the shape does. The page must keep working with no
server at all (the Files app), falling back to browser storage.

## Who it is for

One person, a beginner at the heavy bag, training at home with **1 kg hand weights, maybe 3 kg
ones, and ordinary things around the house** (water bottles, a backpack, a chair, the stairs, a
wall, a book, a towel). Content that needs a gym doesn't belong. Exercises should back up the
bag work: leg drive, core rotation, shoulder endurance, cardio and footwork.

## What is in the pack

| Key | What it holds |
|---|---|
| `punches` | The punch vocabulary: `code` (a number, or `"slip"`), `label`, and optional `easyOn` (the name of its "Go easy on" chip; leave it off for jab and cross). |
| `combos` | Groups of combos: `{ "group", "seqs": [[1,2], [1,1,2], …] }`. A combo is 1 to 8 punch codes. Order matters: the ⟳ button walks through them in this order, so keep similar ones together (lead-hook combos are last on purpose). |
| `defaultRounds` | The combo for each round when the page opens, as punch sequences. Each must also be in `combos`. |
| `equipment` | What can be switched on at setup: `id`, `label`, `on` (its starting state). |
| `warmup` | Guided warm-up moves, in order: `name`, `cue`. Bodyweight only, gentle to brisk. |
| `focusOrder` | The order muscle groups are rotated through when the app picks exercises for you. |
| `defaultExercises` | Exercise ids used for slot 1, 2, 3… when the page opens (this is "this week's" finisher). If one isn't available with the chosen equipment, the app picks another. |
| `exercises` | The exercise bank (below). |
| `workouts` | Ready-made whole sessions (below). The owner steps through them with the Workout picker at the top of the setup screen. |

### An exercise

    { "id": "lateral-raises", "name": "Lateral raises", "focus": "shoulders",
      "cue": "Lift out to the sides to shoulder height, then lower slowly. Soft elbows.",
      "why": "Your shoulders do most of the work in a long round.",
      "needs": [ { "eq": "w1",    "with": "1 kg weights" },
                 { "eq": "house", "with": "water bottles" },
                 { "eq": "w3",    "with": "3 kg weights" } ] }

- `id`: lowercase with hyphens, unique, never reused for a different exercise.
- `name`: short (about 28 characters or fewer). It is the big text on the timer.
- `focus`: `legs`, `core`, `shoulders`, `arms`, `back`, `cardio` or `footwork`. A new word is
  allowed; it is added to the rotation.
- `cue`: how to do it, one or two short sentences, imperative. It sits under the name on a phone
  screen, so keep it to about 140 characters (the checker warns past that).
- `why`: one plain sentence on how it helps the boxing. Shown on the setup screen only, never
  while the clock is running. No health or fitness promises.
- `needs` (optional): equipment that can do the job, **in order of preference**. Each is
  `{ "eq": <an id from "equipment">, "with": <what to say, e.g. "a 3 kg weight at your chest"> }`.
- `bodyweightOk` (optional): `true` when it is fine with no equipment and `needs` only lists
  upgrades (squats, Russian twists). Leave it off when the exercise can't be done without the gear
  (woodchops, lateral raises, chair dips).

How the app uses these: an exercise is offered if it has no `needs`, or `bodyweightOk` is true, or
at least one of its `needs` is switched on. The timer shows the first switched-on option, or
"Bodyweight". Everything is displayed as plain text: **no HTML** in any string.

### A workout

    { "id": "slip-and-counter", "name": "Slip and counter",
      "note": "Practice getting your head off the line, then firing back.",
      "source": "https://…",
      "rounds": [ [1,2], ["slip",2], [1,"slip",2] ],
      "exercises": ["skater-steps", "dead-bugs", "high-knees"] }

- `id`: lowercase with hyphens, unique. `name`: short, it is the headline in the picker.
- `note`: one line on what it trains (about 140 characters or fewer). Plain, no hype.
- `source` (optional): an `https://` page the workout was drawn from. The page shows it as a
  "Source" link and ignores anything that isn't https. Leave it off for one you composed.
- `rounds`: one combo per round, 1 to 30 of them, each **exactly** a sequence that is in
  `combos`. Repeating a combo within a workout is fine. A beginner workout is 5 to 8 rounds.
- `exercises` (optional): exercise ids. They fill the exercise slots when the owner has
  Exercises set to Finisher or Between rounds, in that order. One that the switched-on equipment
  can't do is swapped for another automatically.

Choosing a workout sets the number of rounds, each round's combo and the exercises. It respects
"Go easy on" (a combo the owner has switched off is swapped, and the picker says how many).
Anything the owner then changes by hand shows as "(edited)". Any bad reference (a round that isn't
in `combos`, an unknown exercise id) skips the **whole** workout and shows the amber note.

To research and add workouts, use the **round-timer-workouts** agent
(`.claude/agents/round-timer-workouts.md` at the repo root), which follows these rules.

### Adding

1. Pick the `focus` and write the entry (copy a neighbour).
2. If it needs gear that isn't listed, add it to `equipment` first.
3. Put it next to the others with the same `focus` (the app picks the first of a focus when it
   fills slots).
4. Run the checker. Open the page, set Exercises to Finisher, tap ⟳ on a slot until yours appears.

### Removing

Delete the object. Then check `defaultExercises` (or, for combos, `defaultRounds`) for the id or
sequence, and every workout that uses it: the checker flags anything left dangling. Keep at least 8 exercises that need no
equipment (the checker warns below that), or someone with nothing to hand gets a short list.

## Safety and tone

- Weighted shadow boxing stays at 1 kg or less per hand.
- Prefer the lower-impact version in the cue where a move is jumpy (step, don't jump).
- Beginner-friendly: nothing that needs a spotter, nothing overhead with a heavy weight.
- Voice: plain, direct, no hype. The same register as the buttons ("I'm knackered").

## What not to do here

- Don't add punch codes without a drawing. The silhouettes are drawn in code (`iconSVG`); a code
  with no drawing shows only the guard pose, and the checker warns. That needs a code change, so
  say so rather than slipping it into a content edit.
- Don't add addresses, host names, local paths or secrets: the repo is public.
- Don't delete or reword anything in [REQUESTS.md](REQUESTS.md). New requests from the owner are
  appended there word for word.
- Don't turn the single file into several: it has to keep working from the Files app on an
  iPhone.

## When you're done

Commit to `master` (see the repo's [CLAUDE.md](../../CLAUDE.md)); `tools/trinket check` should pass.
To put it on the Pi: `tools/trinket deploy round-timer`. The artifact copy of the page is
published from the owner's chat; ask them to republish.

[PLAN.md](PLAN.md) phase 2 turns this block into data the server hands to the page (with an MCP
endpoint so agents can set a week's routine). Until then, editing the block is how it is done.
