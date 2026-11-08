#!/usr/bin/env bash
# Refresh the vendored generator from kuraara/designing-stuff (folder bag-tag/), which is where
# the model is designed and versioned. Don't edit bag_tag.py / bag_tag.scad here: change them
# there, then run this.
#   gadgets/bag-tag/generator/sync.sh                     # shallow-clones designing-stuff
#   gadgets/bag-tag/generator/sync.sh ~/src/designing-stuff
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
src="${1:-}"
if [ -z "$src" ]; then
  src="$(mktemp -d)"
  git clone -q --depth 1 https://github.com/kuraara/designing-stuff "$src"
fi
cp "$src/bag-tag/bag_tag.py" "$src/bag-tag/bag_tag.scad" "$here/"
rm -rf "$here/fonts" && cp -r "$src/bag-tag/fonts" "$here/fonts"
commit="$(git -C "$src" log -1 --format='%h %cs %s' -- bag-tag)"
sed -i "s|^Vendored from: .*|Vendored from: designing-stuff ${commit}|" "$here/SOURCE.md"
echo "generator synced: ${commit}"
