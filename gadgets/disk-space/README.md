# Disk Space

What's using the disk on each machine, like `ncdu` or ZTree, for the Pi, the NAS and the PC.

**Status:** the scanner (`scanner/`) runs on the Pi (stack `disk-scan`, port 8130): a full scan
takes about 20 s. The viewer (drill-down, treemap, what
grew, MCP) comes next, then the NAS and Windows scanners.

## The scanner

`diskscan` is one Go program, stdlib only, read-only. It walks a machine's disks on one thread at
the lowest CPU and disk priority and writes a snapshot in
[ncdu's JSON export format](https://dev.yorhel.nl/ncdu/jsonfmt), so any snapshot also opens in
ncdu itself:

```
ncdu -f <(zcat pi-20261109-033000.json.gz)
```

To keep snapshots small, every folder is listed but not every file. In each folder it keeps:

- the 20 biggest files, plus any file of 100 MB or more
- the rest folded into one entry, `(N smaller files)`, with their total size and an `other: N` field

**What it skips:**

- `/proc`, `/sys` and other kernel filesystems
- tmpfs, container layers (overlay), network shares and FUSE (`ALLOW_FS` to include any of these)
- a second mount of a filesystem it has already scanned
- anything listed in `EXCLUDE`

Hard-linked files count once.

```
diskscan scan -root / -o snap.json.gz      # one scan, to a file
diskscan serve                              # the worker (settings below)
```

### The worker (`diskscan serve`)

It scans at `SCAN_AT` and keeps the last `KEEP` snapshots in `/data`. At start-up it scans after
a minute if the newest snapshot is older than `MAX_AGE_HOURS`.

| Call | What |
| --- | --- |
| `GET /api/status` | label, scanning (with progress), last scan's stats, next scan |
| `GET /api/snapshots` | kept snapshots, newest first, with stats |
| `GET /api/snapshots/latest` | the newest snapshot (`.json.gz`) |
| `GET /api/snapshots/{name}` | one snapshot |
| `POST /api/scan` | scan now |

The snapshot and scan calls need `Authorization: Bearer $TOKEN` (or `?token=`), since a snapshot
lists every folder name on the machine. `/api/status` and `/healthz` are open.

| Setting | Default | |
| --- | --- | --- |
| `LABEL` | hostname | which machine, e.g. `pi` |
| `SCAN_ROOT` / `ROOT_NAME` | `/` / `/` | where to scan / what to call it (the Pi mounts its `/` at `/host`) |
| `SCAN_AT`, `SCAN_DAYS` | `03:30`, every day | local time (`TZ`); `SCAN_DAYS=sun` for weekly |
| `MAX_AGE_HOURS` | 26 | |
| `KEEP` | 8 | snapshots kept |
| `KEEP_FILES`, `MIN_FILE_MB` | 20, 100 | file listing per folder (above) |
| `EXCLUDE` | | comma-separated paths as named in the snapshot, e.g. `/var/cache` |
| `ALLOW_FS` | | extra filesystem types to scan, e.g. `fuse` |
| `TOKEN` | | see above |
| `ADDR` | `:8080` | |

### On the Pi

Stack `disk-scan` (`scanner/docker-compose.pi.yml`), port 8130:

- The Pi's `/` is mounted read-only at `/host`.
- It runs with every capability dropped except reading any folder (`DAC_READ_SEARCH`).
- It's limited to `cpus: 0.5`, 256 MB, under `ionice -c3 nice -n19`.
- `TZ` comes from `registry/site.local.json`. `DISKSCAN_TOKEN` comes from
  `~/.config/shiny-trinkets/disk-scan.env` the first time.
- Deploy with `tools/trinket deploy disk-space`. The Dockerfile builds and tests the program
  itself and checks a snapshot opens in ncdu, so it builds on any arm64 or amd64 Docker host.
- The source is at `/src` in the container.
