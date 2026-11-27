# Trinket Watcher

Deploys whatever lands on `master` in this repo, once its checks pass, and keeps every version so
any gadget can be switched back. Page: `https://<pc>:8109` (through the registry); the registry
card has a **Check now** button.

## What it does

Every 5 minutes (`INTERVAL`) it fetches the repo. If `master` moved since the last run:

1. works out what the new commits touched: `gadgets/<id>/…` → that gadget; a `gadget.json`, a
   gadget's icon, anything under `registry/` or `tools/trinket` → the registry too. `infra/` and
   docs are never deployed;
2. runs `tools/trinket check` — if it fails, nothing is deployed;
3. for each changed gadget: its `deploy.test` (a shell command from its `gadget.json`, run in a copy
   of its folder on the build box), then `tools/trinket deploy <id> --existing-only` with
   `TRINKET_REV` set to the commit, so the image is tagged `<image>:<commit>` as well as `:latest`;
4. redeploys the registry if needed, and prunes old versions (keeps `rev0`, the running one and
   the newest `KEEP`=10).

A commit that fails isn't retried by itself; the newest failed run has a **Retry** button (for when the PC or the build box was down). Skipped on purpose: gadgets whose stack
doesn't exist yet (the first deploy is by hand, since a new stack may need secrets) and the
watcher itself (deploying itself would stop it mid-run).

**Versions.** `rev0` is what was running when versioning started (`tools/trinket baseline`). Each
deploy after that is named by its commit. On the page, pick a version and **Switch**: it runs
`tools/trinket use <id> <rev>`, which points `:latest` at that build and redeploys the stack
without building. The next push to that gadget deploys over it again; **Pause** stops that.

## Settings (stack environment)

| | |
|---|---|
| `PORTAINER_URL`, `PORTAINER_API_KEY` | Portainer, with an access token (Portainer → My account → Access tokens) |
| `SITE_LOCAL_JSON` | the contents of `registry/site.local.json` (not in git) |
| `PC_ADDR` | the build box's address (port 2223) |
| `START` | optional: the commit to start after (default: whatever is on master when it first starts) |

On first start it makes an SSH key (`/data/ssh`) and prints the public half (also on the page):
add it to the build box's `AUTHORIZED_KEYS` (stack `buildbox`).

## Working on it

    tools/trinket deploy trinket-watcher    # by hand, from a machine with ssh buildbox

POSTs need `X-Gadget-Action: 1` (as card actions send). The source is at `/src` in the container.
