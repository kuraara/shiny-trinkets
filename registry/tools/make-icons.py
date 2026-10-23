#!/usr/bin/env python3
"""Writes html/_gadgets/icons/*.svg: one style for every app (gradient tile + white glyph).
Run from the gadget-registry folder after adding an app to registry.json."""
import os
import re

S = 'fill="none" stroke="#fff" stroke-width="3.6" stroke-linecap="round" stroke-linejoin="round"'
F = 'fill="#fff"'

# id: (top colour, bottom colour, glyph markup on a 64x64 grid)
ICONS = {
  "gadgets": ("#475569", "#1e293b", f'<g {F}><rect x="14" y="14" width="15" height="15" rx="4"/><rect x="35" y="14" width="15" height="15" rx="4" opacity=".75"/><rect x="14" y="35" width="15" height="15" rx="4" opacity=".75"/><rect x="35" y="35" width="15" height="15" rx="4"/></g>'),
  "video-tools": ("#a855f7", "#6d28d9", f'<rect x="12" y="18" width="40" height="28" rx="4" {S}/><path d="M12 25h40M12 39h40M19 18v7M27 18v7M37 18v7M45 18v7M19 39v7M27 39v7M37 39v7M45 39v7" {S} stroke-width="2.6"/><path d="M29 28.5v7l6-3.5z" {F}/>'),
  "home-assistant": ("#38bdf8", "#0369a1", f'<path d="M14 31L32 15l18 16M19 27v21h26V27" {S}/><circle cx="32" cy="35" r="3.2" {F}/><path d="M32 38v10M32 41l-5-4M32 43l5-4" {S} stroke-width="2.6"/>'),
  "esphome": ("#475569", "#0f172a", f'<rect x="20" y="20" width="24" height="24" rx="3" {S}/><path d="M26 14v6M32 14v6M38 14v6M26 44v6M32 44v6M38 44v6M14 26h6M14 32h6M14 38h6M44 26h6M44 32h6M44 38h6" {S} stroke-width="2.8"/><rect x="27" y="27" width="10" height="10" rx="1.5" {F}/>'),
  "portainer": ("#22d3ee", "#0e7490", f'<path d="M32 12v8M24 20h16" {S}/><rect x="15" y="28" width="14" height="10" rx="1.5" {S} stroke-width="3"/><rect x="35" y="28" width="14" height="10" rx="1.5" {S} stroke-width="3"/><rect x="25" y="40" width="14" height="10" rx="1.5" {S} stroke-width="3"/><path d="M32 20v8" {S} stroke-width="3"/>'),
  "scrypted": ("#f43f5e", "#9f1239", f'<path d="M14 22l26-6 4 14-26 6z" {S}/><path d="M24 35l3 9h-9M44 26l7-3v12l-7-3" {S}/><circle cx="23" cy="25" r="2.4" {F}/>'),
  "plex": ("#facc15", "#ca8a04", f'<rect x="12" y="16" width="40" height="27" rx="4" {S}/><path d="M24 50h16M32 43v7" {S}/><path d="M28 23.5v12l10-6z" {F}/>'),
  "jellyfin": ("#818cf8", "#4338ca", f'<path d="M32 14L50 46H14z" {S}/><path d="M32 29l6.5 11h-13z" {F}/>'),
  "sabnzbd": ("#fbbf24", "#d97706", f'<path d="M32 13v24M22 28l10 10 10-10" {S}/><path d="M14 40v7a3 3 0 0 0 3 3h30a3 3 0 0 0 3-3v-7" {S}/>'),
  "sonarr": ("#60a5fa", "#1d4ed8", f'<rect x="12" y="20" width="40" height="26" rx="4" {S}/><path d="M24 12l8 8 8-8" {S}/><path d="M22 52h20" {S}/>'),
  "radarr": ("#fb923c", "#c2410c", f'<rect x="14" y="26" width="36" height="22" rx="3" {S}/><path d="M14 26l3-10 33 0-3 10" {S}/><path d="M24 16l-3 10M34 16l-3 10M44 16l-3 10" {S} stroke-width="3"/>'),
  "romm": ("#34d399", "#047857", f'<path d="M20 22h24a8 8 0 0 1 8 8v4a10 10 0 0 1-17 7h-6a10 10 0 0 1-17-7v-4a8 8 0 0 1 8-8z" {S}/><path d="M22 28v8M18 32h8" {S} stroke-width="3"/><circle cx="41" cy="29.5" r="2.4" {F}/><circle cx="46" cy="34.5" r="2.4" {F}/>'),
  "glances": ("#4ade80", "#15803d", f'<rect x="12" y="15" width="40" height="30" rx="4" {S}/><path d="M17 33h7l4-9 6 15 4-8h9" {S} stroke-width="3"/><path d="M24 51h16" {S}/>'),
  "claude-code": ("#f59e0b", "#b45309", f'<rect x="12" y="15" width="40" height="34" rx="5" {S}/><path d="M20 26l7 6-7 6M31 40h12" {S}/>'),
  "atv-tv-sync": ("#64748b", "#334155", f'<rect x="10" y="17" width="30" height="21" rx="3" {S}/><path d="M19 45h12" {S}/><rect x="44" y="20" width="10" height="27" rx="5" {S} stroke-width="3"/><circle cx="49" cy="27" r="2.2" {F}/><path d="M25 23.5v5M21.5 25.5a5 5 0 1 0 7 0" {S} stroke-width="2.6"/>'),
  "buildbox": ("#94a3b8", "#475569", f'<path d="M32 12l18 10v20L32 52 14 42V22z" {S}/><path d="M14 22l18 10 18-10M32 32v20" {S}/>'),
  "air-printer": ("#a3a3a3", "#525252", f'<path d="M20 26V14h24v12" {S}/><rect x="12" y="26" width="40" height="16" rx="3" {S}/><path d="M20 38h24v12H20z" {S}/><circle cx="45" cy="32" r="2" {F}/>'),
}

def dedupe(tag):
    # a glyph may override the default stroke width; keep only the last one in each tag
    m = tag.group(0)
    while m.count('stroke-width=') > 1:
        m = re.sub(r'\s*stroke-width="[^"]*"', '', m, count=1)
    return m

def svg(top, bottom, glyph):
    glyph = re.sub(r'<[^>]+>', dedupe, glyph)
    return ('<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64">'
            f'<defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="{top}"/><stop offset="1" stop-color="{bottom}"/></linearGradient></defs>'
            f'<rect width="64" height="64" rx="14" fill="url(#g)"/>{glyph}</svg>\n')

out = os.path.join(os.path.dirname(__file__), '..', 'html', '_gadgets', 'icons')
os.makedirs(out, exist_ok=True)
for name, (a, b, g) in ICONS.items():
    with open(os.path.join(out, f'{name}.svg'), 'w') as f:
        f.write(svg(a, b, g))
print(f'wrote {len(ICONS)} icons')
