# Secret Drop

Hand Claude a password, API key, token or a picture from your phone, without a terminal. Type the
details as `NAME=value` lines and/or paste or pick pictures, give them a name, **Save**. They become
files only Claude can read, in its home on the Pi:

    ~/secrets/<name>.env                      the details (replaced when you save that name again)
    ~/secrets/<name>-img-<time>-<n>.<ext>     each picture (added)

All files are mode 0600, owned by Claude's user. The page only writes: it never shows a value or a
picture again, just each name, the KEY names in its `.env`, how many pictures, and when it changed.
**Delete** removes a name's `.env` and its pictures.

`https://<pc>:8107` through the registry, **HTTPS only** (the registry has the home certificate).
Open `/?key=<token>` once per device (as with Ask Me); after that the plain address works.

## For Claude

Read `~/secrets/<name>.env` like any other env file (`. ~/secrets/hydrawise.env`, or read single
keys), never print the values, and delete the file when it's no longer needed if the user agrees.

## How it runs

A Go service (standard library) on the Pi, port 8132, the registry's upstream. It runs as Claude's
uid/gid with a read-only root, no capabilities, and only `SECRETS_DIR` (from
`registry/site.local.json` `env`) mounted, at `/secrets`.

| | |
|---|---|
| `PUT /api/secrets/<name>` | `{"text": "..."}`, 32 KB at most |
| `POST /api/secrets/<name>/images` | multipart, field `image`, PNG/JPEG/GIF/WebP (sniffed) or HEIC (by name), 25 MB each |
| `GET /api/secrets` | names, keys, picture counts, times; no values |
| `DELETE /api/secrets/<name>` | |

Names: lower-case letters, digits and dashes, starting with a letter. Token: `SECRETDROP_TOKEN`
(stack environment; `~/.config/shiny-trinkets/secret-drop.env` for a new stack).

    go vet ./... && go test ./...      # on the build box
    tools/trinket deploy secret-drop
