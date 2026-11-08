# The generator (vendored)

`bag_tag.py`, `bag_tag.scad` and `fonts/` are copied from
[kuraara/designing-stuff/bag-tag](https://github.com/kuraara/designing-stuff/tree/main/bag-tag),
where the model is designed, printed and versioned. Its README covers the design, every
parameter and the print settings.

Vendored from: designing-stuff a43dbac 2026-11-08 Bag tag v0.1: parametric name + flower, two-colour press-fit inlay

Don't edit these files here. Change them in designing-stuff, then run `generator/sync.sh`.
The gadget treats `bag_tag.py` as a black box with a JSON interface (`--json`, `--params`,
`--list-fonts`), so a new generator version needs no Go changes unless that interface changes.

Fonts are SIL OFL or Apache 2.0 (licence next to each `.ttf`).
