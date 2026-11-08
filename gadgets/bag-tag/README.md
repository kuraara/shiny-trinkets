# Bag Tag

Type a name, pick a font, and get the files for a two-colour school-bag tag (a name hanging
under a flower) to 3D print. The tag is two prints, a yellow base and a white inlay, that press
together, so no multi-material printer is needed. A to-scale preview sits next to an AAA
battery.

**Status: planned.** The geometry and generator are done and tested (`generator/`, from
[designing-stuff/bag-tag](https://github.com/kuraara/designing-stuff/tree/main/bag-tag)). The web
UI, API and MCP are specified in [PLAN.md](PLAN.md). The Go code here is still the template.

`https://<pc>:8106` (through the gadget registry; http:// works too). MCP: `http://<pc>:8106/mcp`.

## Working on it

    gadgets/bag-tag/generator/sync.sh                     # refresh the generator from designing-stuff
    tools/trinket check
    tools/trinket deploy bag-tag
    tools/trinket deploy registry                         # when gadget.json changed
    node test/e2e.js https://<pc>:8106 /tmp/shots         # on the build box
