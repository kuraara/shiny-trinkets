# Working in shiny-trinkets

Read docs/ARCHITECTURE.md first. The short version:

- A gadget = `gadgets/<id>/` with a `gadget.json`. Never hand-edit the registry to add one;
  `tools/trinket new <id> --template go|static` starts it, `tools/trinket check` validates.
- Commit straight to `master` (no feature branches). Never commit secrets: they live in
  `~/.config/shiny-trinkets/<stack>.env` (new stacks) or in the Portainer stack's environment.
- **The Pi never compiles or hosts development work** (it once ran out of memory). Build and test
  on the build box: `ssh -F ~/.ssh/config buildbox` (Go, Node, Chromium + puppeteer-core, the
  PC's Docker). Pi images are cross-compiled there and built on the Pi through Portainer
  (`tools/trinket deploy` does this from a manifest's `build.go`).
- Deploy with `tools/trinket deploy <id>` (stacks are found by name; existing environment is
  kept) and `tools/trinket deploy registry` when a manifest changed.
- Test pages in headless Chromium at 390 px (phone) and 1280 px before calling something done;
  most use is from an iPhone.
- Gadgets talk to each other server-to-server over the `gadgets` Docker network; in the browser,
  link with `gadgets.url(id, path)` (from the navbar script), never hard-coded ports.
- Video Tools: templ is pinned (v0.3.1001); run `templ generate` and commit the `*_templ.go`
  files. Its speaker-naming rules (never identify a real person from their face) are in its MCP
  instructions and README.
