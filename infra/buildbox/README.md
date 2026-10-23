# Build box

A container on the PC that does the heavy lifting so the Pi (4 GB) only plans: Go and Node
builds, tests, headless Chromium for browser tests, and `docker build` against the PC's Docker.

- SSH (key only) on port 2223 as root. Allowed keys come from the stack's `AUTHORIZED_KEYS` variable.
- `/work` and the Go module cache are volumes, so they survive rebuilds.
- It mounts the PC's Docker socket, which is effectively root on the PC's Docker. Keep the key safe.

`redeploy.sh <folder> <stackId>` (run from the Pi): copies a gadget folder over, builds its image
on the PC, and has Portainer redeploy the stack.
