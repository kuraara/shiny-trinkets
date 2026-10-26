# Build box

A container on the PC that does the heavy lifting so the Pi (4 GB) only plans: Go and Node
builds, tests, headless Chromium for browser tests, and `docker build` against the PC's Docker.

- SSH (key only) on port 2223 as root. Allowed keys come from the stack's `AUTHORIZED_KEYS` variable.
- `/work` and the Go module cache are volumes, so they survive rebuilds.
- It mounts the PC's Docker socket, which is effectively root on the PC's Docker. Keep the key safe.

`tools/trinket deploy <gadget>` (run from wherever the repo is checked out, e.g. the Pi) copies
the gadget over, builds its image here against the PC's Docker (or cross-compiles a Go binary
for the Pi), and has Portainer update the stack. Work folders: `/work/trinkets/<gadget>/<stack>`.
The home certificate authority's key also lives here, in `/work/ca` (see infra/ca).

Connecting: `ssh -F ~/.ssh/config buildbox` with a `~/.ssh/config` entry like

    Host buildbox
      HostName <pc>
      Port 2223
      User root
      IdentityFile ~/.ssh/buildbox
