# Photo Drop

A room full of people scan a QR code and send photos from their phones. The host approves
each photo, then it lands on a shared "desk" display (TV or projector): a white-bordered
print, dropped at a random spot and tilted up to ±15°.

- **Start page** (`/`): start a session (real names only? let each guest in yourself?) or join with a code.
- **Host** (`/h/CODE`): approve/reject photos, admit/turn away guests, remove guests, take photos
  off the desk, tidy or shuffle the desk, re-arrange it automatically every few minutes (shuffle or
  tidy grid), download everything as a zip, end the session (deletes all photos).
- **Desk** (`/s/CODE`): the display. Opened from the host page. Shows the join QR code.
  `F` for full screen, click a photo to enlarge it. In the host's own browser the desk also lets you
  drag photos around (mouse or finger), has Tidy / Shuffle / Auto buttons and a "Host controls" link
  back (iPhones have no full screen for web pages, so that link is the way out).
- **Guest** (`/j/CODE`): join, wait to be let in if needed, send photos (with optional captions),
  see what was approved.

Photos are shrunk to 2048 px in the browser before upload and re-encoded on the server, so
location and camera metadata never reach the desk or the disk. Sessions idle for 48 hours are
deleted.

## Running

Go 1.26, no CGO. `go test ./...`; `test/e2e.js` drives the whole flow in headless Chromium
(needs `puppeteer-core`, test pictures in `/tmp/testpics`).

Deployed as the `photo-drop` stack on the PC, reached through the gadget registry on port 8102.

| Env | Default | |
|---|---|---|
| `PUBLIC_URL` | from the request | Base URL for QR codes when hosted behind a public domain |
| `CREATE_PIN` | none | PIN needed to start a session (set this if it's public) |
| `ROOM_TTL_HOURS` | 48 | Idle sessions are deleted after this |
| `MAX_ROOMS` / `MAX_PHOTOS_PER_ROOM` / `MAX_PEOPLE_PER_ROOM` | 50 / 1000 / 500 | |
| `MAX_PHOTOS_PER_PERSON` / `MAX_PENDING_PER_PERSON` | 50 / 10 | |
| `MAX_UPLOAD_MB` | 15 | |
