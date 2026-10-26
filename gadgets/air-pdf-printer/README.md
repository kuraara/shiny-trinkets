# AirPrint to PDF

A printer called **AirPrint PDF @ *the Pi's name*** for iPhones, iPads and Macs on the home
network. Whatever you print is saved as a PDF in a folder on the Pi (`PDF_DIR` in
`registry/site.local.json`), and listed at `http://<pi>:8632` (tap one to open it, or share/save
it from there).

Portainer stack `airprintserver` on the Pi. Image `thyrlian/air-pdf-printer:arm64`
(CUPS + cups-pdf + Avahi) plus a tiny nginx file list.

Why it runs on the Pi: AirPrint is found by mDNS (multicast), which needs host networking on a
Linux Docker host; Docker Desktop on the Windows PC can't pass it through.

Things that got in the way, and what the compose file does about them:

- `latest` is built for amd64 only, so on the Pi it died with "exec format error": use `arm64`.
- The Pi runs its own CUPS on 127.0.0.1:631, so this one couldn't listen on 0.0.0.0:631. It
  listens on the Pi's LAN address (`PI_ADDR`, from the Pi's `addr` in `registry/site.local.json`)
  instead; nothing on the Pi is changed. If the Pi's address changes, change it there and redeploy.
- PDFs are saved straight into the folder (no `ANONYMOUS/` subfolder); the folder is given to
  `nobody`, the user cups-pdf writes as.
- CUPS job history is on a volume, so job numbers (part of each file name) keep counting up and
  two prints with the same title can't overwrite each other.
