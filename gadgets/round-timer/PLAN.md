# Round Timer: plan for a server-side version

**Status: phase 1 done** (9 Nov 2026): served unchanged on the Pi at `http://<pi>:8107` for now, over
plain HTTP; it moves behind the registry's HTTPS when the PC is back. The app exists today as one self-contained page,
[round-timer.html](round-timer.html), and it works on its own. This plan is about hosting it
here as a gadget without losing that. The request is in [REQUESTS.md](REQUESTS.md), word for
word; this page only arranges it. Language and framework are deliberately left open.

## What exists (the artifact)

[round-timer.html](round-timer.html) is the reference for behaviour. It does everything below
in the browser, with no server. Each line is tied to the request it came from (numbers are those
in REQUESTS.md).

- **Sections** (1, 2): warm-up, then a break, then rounds 1..n with rounds added or removed, then
  a break between rounds. Lengths change in 15-second steps (1 asked for 30, 2 changed it to 15).
- **Timer screen** (1): the clock fills most of the screen. White during warm-up and breaks,
  green in a round, orange at 30 s left, red at 10 s.
- **Combos** (2, 4, 5, 15, 16): a bank of 35 heavy-bag combos, one per round. The setup page
  lists them under the config, scrolls, and has a cycle button per round (5). During a break the
  upcoming round's combo is shown (4). Each combo is drawn as little shadow silhouettes (4). A
  "Go easy on" row of chips leaves lead hooks, rear hooks, uppercuts or slips out of the mix
  (15, 16).
- **Ending** (17): an "I'm knackered" button ends the session early, and a stats page follows
  (total time, rounds finished, share of the plan, round time versus rest, each round's combo and
  how long it lasted). Finishing normally shows the same page.
- **Kept as a single file** (1): it still has to run from the Files app on an iPhone with no
  network. The hosted version must not break that.

## Why host it

- The same app at one address on the home network, behind the registry, with HTTPS.
- **Context kept separate from the timer** (10, 11): a weekly routine, and the reasoning for each
  combo, lives outside the timer page. Today that happens in chat and the combos are keyed in by
  hand.
- A place to keep session stats between workouts.
- A microphone hit counter (19), which needs HTTPS (see below).

## Phases

Each phase leaves a working app. Follow [../../docs/ADDING-A-GADGET.md](../../docs/ADDING-A-GADGET.md)
and the rules in [../../CLAUDE.md](../../CLAUDE.md): build on the build box, test at 390 px and
1280 px, no addresses in the repo.

### 1. Serve the artifact unchanged  [done]

- Scaffold the gadget (`tools/trinket new round-timer`); the template choice is open.
- Serve `round-timer.html` as the page, byte for byte. Add `/healthz`.
- Write `gadget.json` (description, a couple of "I want to…" phrases such as "…time a heavy-bag
  session", icon, the navbar as a floating button because the timer is full screen).
- Keep the source readable inside the container image (standing preference), and keep the
  deploy config in git so Portainer versions are easy to manage.
- Check at 390 px and 1280 px, in light and dark, over http and https.

Done when: the page at the gadget's address behaves exactly like the file on the phone.

### 2. Routines as data, separate from the timer

The timer must still work with nothing else running (request 11).

- A routine is: warm-up, round and break lengths, the number of rounds, the combo for each round,
  an optional "go easy on" list, and a short reason for each combo (request 10).
- The timer page asks the server for the current routine and falls back to its built-in defaults
  if there isn't one or the server is unreachable.
- The reasons show somewhere that never gets in the way of the clock: the setup page and the
  break screen, not the running round.
- Routines are written by Claude from chat or through an MCP endpoint, so "give me a power week"
  becomes a routine the timer picks up. Read-only for the timer.
- The bank of combos and the punch drawings stay in the timer page; a routine refers to combos by
  their punch sequence, not by position in a list.

### 3. Session history

- On the stats page, save the session (when, planned versus actual, per-round time and combo).
- A history view: sessions per week, total time, rounds finished.
- Stored on the server, private, no accounts. Decide how long to keep it.

### 4. Microphone hit counter

Request 19. **Spike first** (see below), then fold into the timer if it holds up on the phone.

## Microphone hit counter: is it possible?

**Short answer: yes for counting impacts, with limits.** It can count bag impacts. It can't tell
a jab from a hook, or a good hit from a glancing one.

### What I checked

- **Browser API.** `getUserMedia` gives the page the microphone. Browsers only offer it in a
  secure context (HTTPS, or localhost) [per MDN]. On the iPhone, Safari supports it.
- **From the Files app.** Not verified. I can't test an iPhone from here, and a bare `.html`
  file opened from Files may not be a secure context or may not be allowed to ask. Treat it as
  unreliable.
- **Inside the claude.ai artifact.** The artifact runtime here doesn't offer a microphone
  capability, so don't expect it in chat.
- **Hosted here.** The registry already serves every gadget over HTTPS with the home certificate
  authority. If the phone trusts that certificate, the page is a secure context and the
  microphone prompt appears. That is a good reason to host it, and the main reason to do this
  phase after phase 1.
- **A working prototype of the counting.** A small detector (below) was run in headless Chromium
  with a fake microphone playing a 24-second synthetic recording: room noise plus mains hum,
  16 "thumps" (some in quick runs 0.15–0.3 s apart, three of them quiet), and four timer-style
  880 Hz beeps. Result: **15 of 16 hits counted** (13 of 13 loud, 2 of 3 quiet), **0 false
  triggers**, and the 4 beeps registered as onsets, which the app can ignore because it knows
  when it plays them. **This is synthetic audio, not a real bag.** It shows the approach
  works end to end; it says nothing yet about how a real bag, room or phone position behaves.

### How the counter would work

1. Ask for the microphone with echo cancellation, noise suppression and automatic gain **off**.
   Those are meant for voice calls and flatten exactly the sharp sounds being counted.
2. Read the level (RMS of the last ~1000 samples) on every frame.
3. Keep a slow estimate of the room's noise floor, learned only from quiet frames.
4. Count a hit when the level jumps well above that floor and is rising, then ignore the next
   ~120 ms so one thump isn't counted twice.
5. Ignore a short window around the app's own beeps.
6. Add a short calibration ("hit the bag three times") and a sensitivity slider, because the
   right threshold depends on the bag, the room and where the phone sits.
7. Show a live level meter so it's obvious when it's listening, and count per round and in total.
   Put the counts on the stats page.

### Limits to expect

- It counts **impacts**, not punches. A miss makes no sound. Two punches closer together than
  the ignore window count as one.
- No punch types, no strength, no accuracy. (Loudness is a rough stand-in for power at best.)
- Anything else that is sudden and loud counts too: a chain clinking, a shout, a door,
  music with a hard beat. Nearby speakers playing the beeps are handled by rule 5; other music is
  not.
- Where the phone sits matters (on the floor near the bag, not on the bag).
- On iPhone, opening the microphone can change how audio plays (volume or output route) and may
  ask for permission each time. **Needs a test on the actual phone** before relying on the beeps.
- The screen must stay awake; the timer already asks for that.

### Privacy

All of it runs in the browser. Audio is never sent anywhere or stored; only the counts are kept,
and only if phase 3 is built.

### Spike (do before building)

Add a "Listen" test page to the gadget: asks for the microphone, shows the live level and a hit
counter, with the sensitivity slider. Try it with the real bag on the real phone, over HTTPS
through the registry. If it counts well, build it into the timer; if not, this is cheap to drop.

## Open questions

- Should the routine be one at a time ("this week"), or several kept and picked from a list?
- How long to keep session history?
- Is the file-on-the-phone version still wanted once this is hosted? (Request 1 asked for it;
  the plan assumes yes, so it stays working and nothing here depends on the server.)
- Does the phone already trust the home certificate? If not, the microphone won't be available
  there.

## Not in this plan

Choice of language or framework, port number, and the deploy details. Those get settled when
phase 1 starts.

[per MDN]: https://developer.mozilla.org/en-US/docs/Web/API/MediaDevices/getUserMedia
