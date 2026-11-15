# ATV TV Sync actions

The small web endpoint behind the buttons on the ATV TV Sync card (see `actions` in
[docs/ARCHITECTURE.md](../../docs/ARCHITECTURE.md)):

- **TV power**: sends the TV's IR power toggle, at any hour.
- **White noise**: plays generated noise on the Apple TV, which sends it to its usual output (the
  soundbar). No phone and no app on the Apple TV. Press again to stop.

ATV TV Sync itself (`monitor.py`, which turns the TV on and off with the Apple TV) is not in this
repo: it holds the Apple TV pairing. This image is built on top of its image and imports it for the
Apple TV's address, the pairing and the TV's IR code.

## White noise

- `noise.py` makes the noise as it is read (white, pink or brown; no audio file) and streams it over
  AirPlay with pyatv. It fades in over 3 s and stops itself after `NOISE_MINUTES` (10 h).
- **The TV stays off.** When a noise start wakes the Apple TV, the TV sync asks `GET /noise`, skips
  that wake's TV signal, and skips the matching sleep's too (the TV's IR code is a toggle, so an
  "off" would turn it on). This holds outside quiet hours as well.
- **Afterwards** the Apple TV is put back to sleep if the noise is what woke it and the noise ended
  on purpose (stopped, or the timer). If something cut the stream off, it's left alone.
- If you use the Apple TV during a noise session, the TV doesn't come on by itself: press TV power.
  (Guessing "someone is watching" from playback reports could switch the TV on at night.)

| Call | |
| --- | --- |
| `POST /noise/toggle` | start, or stop if it's playing (the button) |
| `POST /noise/on`, `POST /noise/off` | |
| `GET /noise` | state, minutes left, whether a wake right now is the noise's doing |
| `POST /tv/power` | the TV toggle, retried up to 3 times |

POSTs need the header `X-Gadget-Action: 1`. Query parameters for a start: `minutes`, `color`
(`white`, `pink`, `brown`), `level` (0 to 1) and `volume` (AirPlay volume, 0 to 100). `?dry=1`
checks everything short of sending or playing.

From anything on the home network (a button box, Home Assistant), go through the registry:

    curl -X POST -H 'X-Gadget-Action: 1' http://<pc>/_gadgets/action/atv-tv-sync/white-noise

| Setting | Default | |
| --- | --- | --- |
| `NOISE_MINUTES` | 600 | it stops itself after this long (at most 720) |
| `NOISE_COLOR` | white | |
| `NOISE_LEVEL` | 0.2 | loudness of the noise itself, before the soundbar's volume |
| `NOISE_VOLUME` | | AirPlay volume; empty leaves it as the Apple TV has it |
| `NOISE_SLEEP_AFTER` | 1 | put the Apple TV back to sleep when the noise woke it |

AirPlay needs the Apple TV to reach the sender on two UDP ports (timing and resend requests), so
they are fixed (16100 and 16101) and published by the compose file.

## Build and deploy

On the build box, in a folder holding `monitor.py` from the atv-tv-sync container next to these files:

    python3 noise.py selftest                      # the generator alone; contacts no device
    DOCKER_BUILDKIT=0 docker build -t atv-actions:latest .
    docker run --rm --network none atv-actions:latest python3 noise.py selftest   # with pyatv's reader

Then update the `atv-actions` stack in Portainer with `docker-compose.yml`. Rebuild whenever
`monitor.py` changes.
