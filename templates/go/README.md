# __NAME__

One paragraph: what it does and who it's for.

`https://<pc>:__PORT__` (through the gadget registry; http:// works too).

## Working on it

    tools/trinket check            # the manifest is fine
    tools/trinket deploy __ID__    # build on the build box, create/update the Portainer stack
    tools/trinket deploy registry  # only when gadget.json changed (name, icon, port, intents…)
    node test/e2e.js https://<pc>:__PORT__ /tmp/shots   # on the build box

See docs/ADDING-A-GADGET.md in the repo root.
