"""White noise for the Apple TV: made up here, sent over AirPlay, and played through whatever the
Apple TV's sound goes to (the soundbar). No phone, no app on the Apple TV, no audio file.

actions.py runs a Player; the TV sync (monitor.py) asks GET /noise so it can leave the TV alone
when the Apple TV wakes up for this.

  python3 noise.py selftest    check the generator and how pyatv reads it; contacts no device
"""
import array
import asyncio
import concurrent.futures
import io
import logging
import random
import struct
import sys
import threading
import time

log = logging.getLogger("atv-noise")

RATE = 44100               # the generated audio: mono, 16-bit
HEADER = 44
MAX_SECONDS = 12 * 3600    # a WAV's size field is 32 bits: room for about 13.5 h of this
COLORS = ("white", "pink", "brown")
FADE_IN = 3 * RATE         # samples: it comes up over 3 s rather than starting at full level
# pink and brown come out of their filters at other levels than white; these bring all three to
# the same RMS (measured over 400,000 samples)
PINK_GAIN = 0.339
BROWN_GAIN = 10.06
# The Apple TV sends timing requests and resend requests back to these UDP ports, so they are
# fixed and published by the container (see docker-compose.yml). 0 = any free port.
CONTROL_PORT = 16100
TIMING_PORT = 16101


class Generator:
    """Noise samples, a block at a time. level: 0 to 1 of full scale."""

    def __init__(self, color: str = "white", level: float = 0.2) -> None:
        if color not in COLORS:
            raise ValueError(f"colour must be one of {', '.join(COLORS)}")
        self.color = color
        self.amp = max(0.0, min(float(level), 1.0)) * 32767
        self.made = 0                 # samples so far, for the fade-in
        self.state = [0.0, 0.0, 0.0]  # filter memory
        self.random = random.Random().random

    def block(self, n: int) -> array.array:
        r = self.random
        if self.color == "pink":      # Paul Kellet's economy filter: about -3 dB per octave
            b0, b1, b2 = self.state
            out = []
            for _ in range(n):
                w = r() * 2 - 1
                b0 = 0.99765 * b0 + w * 0.0990460
                b1 = 0.96300 * b1 + w * 0.2965164
                b2 = 0.57000 * b2 + w * 1.0526913
                out.append((b0 + b1 + b2 + w * 0.1848) * PINK_GAIN)
            self.state = [b0, b1, b2]
        elif self.color == "brown":   # a leaky integrator: about -6 dB per octave
            y = self.state[0]
            out = []
            for _ in range(n):
                y = (y + 0.02 * (r() * 2 - 1)) / 1.02
                out.append(y * BROWN_GAIN)
            self.state[0] = y
        else:
            out = [r() * 2 - 1 for _ in range(n)]
        amp, start = self.amp, self.made
        self.made += n
        if start < FADE_IN:
            out = [v * amp * min(1.0, (start + i) / FADE_IN) for i, v in enumerate(out)]
        else:
            out = [v * amp for v in out]
        return array.array("h", [32767 if v > 32767 else -32767 if v < -32767 else int(v) for v in out])


class NoiseWav(io.BufferedIOBase):
    """A WAV file that isn't there: a header saying how long it is, then noise made as it's read.

    It can seek (the noise after a seek is just different noise), so pyatv and miniaudio treat it
    like a file on disk. Setting .stop ends it early: reads then return nothing, which is how a
    stream is stopped cleanly.
    """

    def __init__(self, seconds: float, color: str = "white", level: float = 0.2) -> None:
        super().__init__()
        self.seconds = max(1, min(int(seconds), MAX_SECONDS))
        data = self.seconds * RATE * 2
        self.size = HEADER + data
        self._header = struct.pack("<4sI4s4sIHHIIHH4sI", b"RIFF", self.size - 8, b"WAVE", b"fmt ", 16,
                                   1, 1, RATE, RATE * 2, 2, 16, b"data", data)
        self._pos = 0
        self._buf = b""
        self._gen = Generator(color, level)
        self.stop = threading.Event()
        self.played = 0               # audio bytes handed out so far
        self.name = "noise.wav"       # used in other libraries' messages

    def readable(self) -> bool:
        return True

    def seekable(self) -> bool:
        return True

    def tell(self) -> int:
        return self._pos

    def seek(self, offset: int, whence: int = io.SEEK_SET) -> int:
        base = {io.SEEK_SET: 0, io.SEEK_CUR: self._pos, io.SEEK_END: self.size}[whence]
        new = max(0, min(self.size, base + offset))
        if new != self._pos:
            self._pos, self._buf = new, b""
        return self._pos

    @property
    def exhausted(self) -> bool:
        return self._pos >= self.size

    def read(self, size: int = -1) -> bytes:
        if size is None or size < 0:
            size = 1 << 16            # never "the whole file"
        if self.stop.is_set():
            return b""
        size = min(size, self.size - self._pos)
        if size <= 0:
            return b""
        out = self._header[self._pos:self._pos + size] if self._pos < HEADER else b""
        need = size - len(out)
        if need > 0:
            if not self._buf and (self._pos + len(out) - HEADER) % 2:
                self._buf = b"\0"     # after a seek into the middle of a sample: stay aligned
            while len(self._buf) < need:
                self._buf += self._gen.block(4096).tobytes()
            out += self._buf[:need]
            self._buf = self._buf[need:]
            self.played += need
        self._pos += len(out)
        return out


def describe(exc: BaseException) -> str:
    """An error in words for the button's message."""
    name = type(exc).__name__
    if name == "AuthenticationError":
        return "the Apple TV refused the sign-in for audio (it may need pairing for AirPlay audio)"
    text = str(exc) or name
    return text if len(text) <= 160 else text[:157] + "..."


class Player:
    """One noise session at a time, on its own event loop in a background thread."""

    START_WINDOW = 120    # s after a start during which an Apple TV wake counts as caused by it

    def __init__(self, host: str, companion_cred: str, airplay_cred: str, sleep_after: bool = True) -> None:
        self.host, self._companion, self._airplay = host, companion_cred, airplay_cred
        self.sleep_after = sleep_after
        self.loop = asyncio.new_event_loop()
        threading.Thread(target=self.loop.run_forever, name="noise", daemon=True).start()
        self.lock = threading.Lock()
        self.state = "stopped"        # stopped | starting | playing | stopping
        self.session = None           # concurrent.futures.Future of the running session
        self.src = None
        self.atv = None
        self.started = 0.0            # when the last start was asked for
        self.window_until = 0.0
        self.ends = 0.0
        self.woke = False             # the Apple TV was asleep when this session started
        self.error = ""
        self.cfg = {}
        self._ready = threading.Event()
        self._stop_asked = False

    # ---- what the HTTP side calls (any thread)

    def status(self) -> dict:
        with self.lock:
            now = time.time()
            active = self.state != "stopped"
            return {
                "state": self.state,
                "playing": self.state == "playing",
                # for the TV sync: did an Apple TV wake right now come from a noise start?
                "wake_is_noise": now < self.window_until,
                # ...and is the Apple TV awake only because of the noise?
                "woke_apple_tv": active and self.woke,
                "minutes_left": max(0, round((self.ends - now) / 60)) if active else 0,
                "color": self.cfg.get("color"), "level": self.cfg.get("level"),
                "error": self.error,
            }

    def start(self, minutes: int, color: str, level: float, volume=None, wait: float = 45) -> tuple:
        """Start a session. Returns (ok, message) once it's playing, has failed, or wait is up."""
        with self.lock:
            if self.state != "stopped":
                return True, f"Already {self.state}"
            now = time.time()
            self.state, self.error, self.woke, self._stop_asked = "starting", "", False, False
            self.started, self.window_until, self.ends = now, now + self.START_WINDOW, now + minutes * 60
            self.cfg = {"minutes": minutes, "color": color, "level": level, "volume": volume}
            self._ready.clear()
            self.session = asyncio.run_coroutine_threadsafe(self._session(minutes, color, level, volume), self.loop)
        self._ready.wait(wait)
        with self.lock:
            if self.state == "playing":
                length = f"{minutes} min" if minutes < 120 else f"{minutes / 60:g} h"
                return True, f"Playing (stops itself after {length})"
            if self.state == "starting":
                return True, "Still starting; press again in a moment to check or stop"
            return False, f"Couldn't start: {self.error}" if self.error else "Stopped before it started"

    def stop(self, wait: float = 15) -> tuple:
        with self.lock:
            if self.state == "stopped":
                return True, "Not playing"
            was, self.state, self._stop_asked = self.state, "stopping", True
            src, atv, session = self.src, self.atv, self.session
        if src is not None:
            src.stop.set()            # the stream runs out and ends normally
        if atv is not None and was == "playing":
            asyncio.run_coroutine_threadsafe(self._remote_stop(atv), self.loop)
        try:
            session.result(timeout=wait)
        except concurrent.futures.TimeoutError:
            log.warning("The stream didn't end by itself in %ss; cancelling it", wait)
            session.cancel()
            try:
                session.result(timeout=5)
            except Exception:
                pass
        except Exception:
            pass
        return True, "Stopped"

    def toggle(self, minutes: int, color: str, level: float, volume=None) -> tuple:
        if self.status()["state"] == "stopped":
            return self.start(minutes, color, level, volume)
        return self.stop()

    def dry_run(self) -> float:
        """Make 2 s of noise and read it the way a stream would. Contacts nothing. Returns seconds read."""
        return asyncio.run_coroutine_threadsafe(decode_check(2), self.loop).result(timeout=30)

    # ---- on the noise loop

    @staticmethod
    async def _remote_stop(atv) -> None:
        try:
            await atv.remote_control.stop()   # while streaming, this is pyatv's "stop the stream"
        except Exception:
            pass

    async def _session(self, minutes: int, color: str, level: float, volume) -> None:
        import pyatv
        from pyatv.const import PowerState, Protocol
        from pyatv.interface import MediaMetadata
        from pyatv.storage.memory_storage import MemoryStorage

        atv = src = stream = None
        woke = finished = False
        try:
            confs = []
            for _ in range(2):        # it can miss the first ask when it's deep asleep
                confs = await pyatv.scan(self.loop, hosts=[self.host], timeout=8)
                if confs:
                    break
            if not confs:
                raise RuntimeError("the Apple TV didn't answer on the network")
            conf = confs[0]
            conf.set_credentials(Protocol.Companion, self._companion)
            conf.set_credentials(Protocol.AirPlay, self._airplay)
            conf.set_credentials(Protocol.RAOP, self._airplay)   # audio streaming signs in the same way
            storage = MemoryStorage()
            settings = await storage.get_settings(conf)
            settings.protocols.raop.control_port = CONTROL_PORT
            settings.protocols.raop.timing_port = TIMING_PORT
            atv = await pyatv.connect(conf, self.loop, storage=storage)
            self.atv = atv
            await asyncio.sleep(1.5)  # its power state arrives just after connecting
            woke = atv.power.power_state == PowerState.Off
            with self.lock:
                self.woke = woke
            if self._stop_asked:
                return
            src = NoiseWav(minutes * 60, color, level)
            self.src = src
            log.info("Starting %s noise for up to %d min (level %.2f; the Apple TV was %s)",
                     color, minutes, level, "asleep" if woke else "awake")
            stream = asyncio.ensure_future(atv.stream.stream_file(
                src, metadata=MediaMetadata(title=f"{color.capitalize()} noise", artist="Gadgets", album="Noise")))
            while not stream.done():
                await asyncio.wait([stream], timeout=0.5)
                if self.state == "starting" and src.played > RATE * 2 * 2:   # 2 s of it has gone out
                    with self.lock:
                        if self.state == "starting":
                            self.state = "playing"
                    self._ready.set()
                    log.info("Playing")
                    if volume is not None:
                        try:
                            await atv.audio.set_volume(float(volume))
                        except Exception as exc:
                            log.warning("Couldn't set the volume to %s: %r", volume, exc)
            await stream              # raises here if the stream failed
            finished = src.stop.is_set() or src.exhausted
            log.info("Noise ended (%s)", "stopped" if src.stop.is_set() else
                     "timer" if src.exhausted else "cut off by the Apple TV")
        except asyncio.CancelledError:
            log.info("Noise session cancelled")
        except Exception as exc:
            self.error = describe(exc)
            log.exception("Noise session failed")
        finally:
            if stream is not None and not stream.done():
                stream.cancel()
                try:
                    await stream
                except BaseException:
                    pass
            if atv is not None:
                # Only when it was asleep before, and the noise ended on purpose: if something cut
                # the stream off, someone may be using the Apple TV.
                if finished and woke and self.sleep_after:
                    try:
                        await atv.power.turn_off()
                        log.info("Put the Apple TV back to sleep (it was asleep before the noise)")
                    except Exception as exc:
                        log.warning("Couldn't put the Apple TV back to sleep: %r", exc)
                try:
                    tasks = atv.close()
                    if tasks:
                        await asyncio.gather(*tasks, return_exceptions=True)
                except Exception:
                    pass
            with self.lock:
                self.state, self.atv, self.src = "stopped", None, None
                self.window_until = min(self.window_until, time.time() + 15)
            self._ready.set()


async def decode_check(seconds: int = 2, color: str = "white", level: float = 0.2) -> float:
    """Read a NoiseWav through pyatv's own audio source, as a stream would. Returns seconds read."""
    from pyatv.protocols.raop.audio_source import open_source
    source = await open_source(NoiseWav(seconds, color, level), 44100, 2, 2)
    frames = 0
    try:
        while True:
            data = await source.readframes(352)
            if not data:
                break
            frames += len(data) // 4
    finally:
        await source.close()
    return frames / 44100


def selftest() -> int:
    """Checks that need no Apple TV. Prints what it finds; returns the number of problems."""
    import math
    import wave
    bad = 0

    def check(ok: bool, what: str) -> None:
        nonlocal bad
        bad += 0 if ok else 1
        print(("ok   " if ok else "FAIL ") + what)

    for color in COLORS:
        t0 = time.perf_counter()
        with wave.open(NoiseWav(10, color, 0.2)) as w:
            fmt = (w.getnchannels(), w.getsampwidth(), w.getframerate(), w.getnframes())
            samples = array.array("h", w.readframes(w.getnframes()))
        took = time.perf_counter() - t0
        body = samples[FADE_IN:]
        rms = math.sqrt(sum(v * v for v in body) / len(body)) / 32767
        peak = max(abs(v) for v in body) / 32767
        check(fmt == (1, 2, RATE, 10 * RATE) and len(samples) == 10 * RATE, f"{color}: a valid 10 s WAV {fmt}")
        check(0.09 < rms < 0.14, f"{color}: level {rms:.3f} RMS (expect about 0.115), peak {peak:.2f}")
        check(abs(samples[0]) < 300 and max(abs(v) for v in samples[:2000]) < 1000, f"{color}: fades in")
        check(took < 5, f"{color}: 10 s made in {took:.2f} s")

    long = NoiseWav(MAX_SECONDS)
    check(long.size < 2 ** 32 and long.seek(0, io.SEEK_END) == long.size and long.read(8) == b"", "12 h fits a WAV header")
    odd = NoiseWav(5)
    odd.seek(HEADER + 1001)
    check(len(odd.read(999)) == 999 and odd.tell() == HEADER + 2000, "reads after an odd seek")
    stopping = NoiseWav(60)
    stopping.read(5000)
    stopping.stop.set()
    check(stopping.read(4096) == b"", "stop makes it run out")

    try:
        import pyatv  # noqa: F401
    except ImportError:
        print("skip pyatv checks (pyatv isn't installed here)")
        return bad

    async def with_pyatv() -> None:
        from pyatv.conf import AppleTV, ManualService
        from pyatv.const import Protocol
        from pyatv.protocols.raop.audio_source import open_source
        from pyatv.storage.memory_storage import MemoryStorage
        from pyatv.support.metadata import get_metadata

        meta = await get_metadata(NoiseWav(7))
        check(meta.duration is not None and abs(meta.duration - 7) < 0.1, f"pyatv reads its length ({meta.duration} s)")
        for color in COLORS:
            got = await decode_check(3, color)
            check(abs(got - 3) < 0.3, f"pyatv decodes {color} to stereo 44.1 kHz ({got:.2f} of 3 s)")
        src = NoiseWav(600)
        source = await open_source(src, 44100, 2, 2)
        first = await source.readframes(352)
        src.stop.set()
        more = 0
        while True:
            data = await source.readframes(352)
            if not data:
                break
            more += len(data) // 4
        await source.close()
        check(len(first) == 352 * 4 and more < 3 * 44100, f"a stream ends within {more / 44100:.2f} s of stop")
        conf = AppleTV("127.0.0.1", "test")
        conf.add_service(ManualService("test-id", Protocol.RAOP, 7000, {}))
        storage = MemoryStorage()
        settings = await storage.get_settings(conf)
        settings.protocols.raop.control_port, settings.protocols.raop.timing_port = CONTROL_PORT, TIMING_PORT
        again = await storage.get_settings(conf)
        check(again.protocols.raop.timing_port == TIMING_PORT and again.protocols.raop.control_port == CONTROL_PORT,
              "the fixed UDP ports reach pyatv's settings")

    asyncio.run(with_pyatv())
    return bad


if __name__ == "__main__":
    if sys.argv[1:] == ["selftest"]:
        problems = selftest()
        print("all good" if not problems else f"{problems} problem(s)")
        sys.exit(1 if problems else 0)
    print(__doc__)
