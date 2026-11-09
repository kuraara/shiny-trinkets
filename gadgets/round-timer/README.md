# Round Timer

A heavy-bag round timer: warm-up, rounds and breaks, with a combo for each round and a stats page
at the end. The page is [round-timer.html](round-timer.html), served unchanged; the same file
also works on its own from the Files app on an iPhone. The request log is in
[REQUESTS.md](REQUESTS.md), and the phases for the hosted version in [PLAN.md](PLAN.md).

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
