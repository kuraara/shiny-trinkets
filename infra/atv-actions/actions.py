"""ATV TV Sync actions: a tiny web endpoint behind the gadget page's buttons.

  POST /tv/power        send the TV power toggle (same as `monitor.py toggle`; ignores quiet hours),
                        retrying the living room blaster, which sometimes misses the first packet
  POST /noise/toggle    start white noise on the Apple TV, or stop it if it's playing
  POST /noise/on        start it      ?minutes=600&color=white|pink|brown&level=0.2&volume=30
  POST /noise/off       stop it
  GET  /noise           what the noise is doing (the TV sync reads this; see monitor.py)
  GET  /healthz

Add ?dry=1 to a POST to check the plumbing without sending or playing anything.

POSTs need the header "X-Gadget-Action: 1" (the gadget registry adds it), so a link or a form on
another page can't trigger them. Nothing here is published except the two UDP ports AirPlay
needs; the registry reaches it on the gadgets Docker network.
It reuses monitor.py from the atv-tv-sync image for the Apple TV's address, pairing and IR code.
"""
import json
import logging
import os
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlsplit

import monitor
import noise

log = logging.getLogger("atv-actions")
busy = threading.Lock()


def env(name: str, default: str) -> str:
    return os.environ.get(name) or default


# Defaults for the button; a request can override them with query parameters.
NOISE_MINUTES = int(env("NOISE_MINUTES", "600"))      # stops itself after this long
NOISE_COLOR = env("NOISE_COLOR", "white")
NOISE_LEVEL = float(env("NOISE_LEVEL", "0.2"))        # 0 to 1 of full scale, before the soundbar's own volume
NOISE_VOLUME = os.environ.get("NOISE_VOLUME") or None  # AirPlay volume 0-100; unset = leave it as the Apple TV has it
player = noise.Player(monitor.ATV_HOST, monitor.ATV_COMPANION_CRED, monitor.ATV_AIRPLAY_CRED,
                      sleep_after=env("NOISE_SLEEP_AFTER", "1") == "1")


class Handler(BaseHTTPRequestHandler):
    def reply(self, code, body):
        b = json.dumps(body).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Cache-Control", "no-store")
        self.send_header("Content-Length", str(len(b)))
        self.end_headers()
        self.wfile.write(b)

    def do_GET(self):
        path = urlsplit(self.path).path
        if path == "/healthz":
            return self.reply(200, {"ok": True})
        if path == "/noise":
            return self.reply(200, player.status())
        self.reply(404, {"error": "not found"})

    def do_POST(self):
        u = urlsplit(self.path)
        q = {k: v[-1] for k, v in parse_qs(u.query).items()}
        if u.path not in ("/tv/power", "/noise/toggle", "/noise/on", "/noise/off"):
            return self.reply(404, {"error": "no such action"})
        if self.headers.get("X-Gadget-Action") != "1":
            return self.reply(403, {"error": "missing X-Gadget-Action header"})
        if u.path == "/tv/power":
            return self.tv_power(q)
        return self.noise(u.path.rsplit("/", 1)[1], q)

    def tv_power(self, q):
        if q.get("dry") == "1":
            return self.reply(200, {"message": "Ready (dry run, nothing sent)"})
        if not busy.acquire(blocking=False):
            return self.reply(409, {"message": "Already sending"})
        try:
            err = None
            for attempt in range(1, 4):
                try:
                    monitor.send_tv_power_toggle()
                    log.info("TV power toggle sent from the gadget page (attempt %d)", attempt)
                    return self.reply(200, {"message": "Sent" if attempt == 1 else f"Sent (try {attempt})"})
                except Exception as e:  # the blaster times out now and then
                    err = e
                    time.sleep(2)
            return self.reply(502, {"error": f"The living room IR blaster didn't answer: {err}"})
        finally:
            busy.release()

    def noise(self, what, q):
        try:
            minutes = max(1, min(int(q.get("minutes", NOISE_MINUTES)), noise.MAX_SECONDS // 60))
            color = q.get("color", NOISE_COLOR)
            level = max(0.0, min(float(q.get("level", NOISE_LEVEL)), 1.0))
            volume = q.get("volume", NOISE_VOLUME)
            volume = None if volume is None else max(0.0, min(float(volume), 100.0))
            if color not in noise.COLORS:
                raise ValueError(f"colour must be one of {', '.join(noise.COLORS)}")
        except ValueError as e:
            return self.reply(400, {"error": str(e)})
        if q.get("dry") == "1":
            try:
                secs = player.dry_run()
            except Exception as e:
                log.exception("Noise dry run failed")
                return self.reply(500, {"error": f"Dry run failed: {noise.describe(e)}"})
            state = player.status()["state"]
            return self.reply(200, {"message": f"Ready (dry run, nothing played; {state} now)", "seconds": round(secs, 2)})
        if what == "off":
            ok, msg = player.stop()
        elif what == "on":
            ok, msg = player.start(minutes, color, level, volume)
        else:
            ok, msg = player.toggle(minutes, color, level, volume)
        log.info("Noise %s from the gadget page: %s", what, msg)
        self.reply(200, {"message": msg}) if ok else self.reply(502, {"error": msg})

    def log_message(self, fmt, *args):
        if "/noise " not in fmt % args and "/healthz" not in fmt % args:   # the TV sync's polls would fill the log
            log.info("%s %s", self.address_string(), fmt % args)


if __name__ == "__main__":
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
    log.info("atv-actions listening on :8080 (noise: %s, %d min, level %.2f)", NOISE_COLOR, NOISE_MINUTES, NOISE_LEVEL)
    ThreadingHTTPServer(("", 8080), Handler).serve_forever()
