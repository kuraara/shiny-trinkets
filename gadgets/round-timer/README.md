# Round Timer

A heavy-bag round timer: a guided warm-up, rounds and breaks, a combo for each round, optional
strength and cardio exercises (from bodyweight to light weights and things around the house), and
a stats page at the end. The page is [round-timer.html](round-timer.html), served unchanged; the same file
also works on its own from the Files app on an iPhone. The request log is in
[REQUESTS.md](REQUESTS.md), and the phases for the hosted version in [PLAN.md](PLAN.md).

**Content for other agents.** Combos, exercises, warm-up moves and equipment are plain JSON
inside the page. [AGENTS.md](AGENTS.md) says how to add or remove them, and
`python3 tools/check_content.py round-timer.html` checks the result.

**Workouts.** The Workout picker at the top of the setup screen steps through ready-made whole
sessions (a combo per round plus finisher exercises), so there's no swapping drills one round at
a time. The `round-timer-workouts` agent (`.claude/agents/` at the repo root) researches new ones
and adds them to the content pack.

**Now:** phase 1, on the Pi at `http://<pi>:8107` (plain HTTP, its own port). Because it isn't
HTTPS, the phone may not keep the screen awake (browsers only allow the wake lock on secure pages).
That and the microphone (phase 4) wait for the move behind the registry's HTTPS on the PC.

## Working on it

    tools/trinket check
    tools/trinket deploy round-timer       # builds through Portainer on the Pi
    tools/trinket deploy registry          # when gadget.json changes
    node test/e2e.js http://<pi>:8107 /tmp/shots   # on the build box: "/" is the file byte for byte,
                                                   # 390/1280 px, light/dark, a session start to stats

The source is at `/src` in the container.
