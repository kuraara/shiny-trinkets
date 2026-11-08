// Bag tag: a name hanging under a flower, as a two-colour inlay for a single-extruder printer.
// Print the BASE (colour 1, pockets facing up) and the INLAY (colour 2) separately,
// then press the inlay into the pockets.
//
// The letters stay separate on the face. A hidden bar ("sprue") under them, sitting in a
// groove in the base's floor, holds the whole name together as ONE inlay piece. With the
// sprue the inlay prints face-down (bar on top). sprue = false gives loose letters that print
// face-up.
//
// Parts: assembly | base | inlay (print orientation) | plate | outline2d | pocket2d | inlay2d
//        | sprue2d | slab2d | probe | info
// Units mm. Flower centre at the origin, the tag hangs along -Y, Z up, base on Z = 0.
//
// bag_tag.py (next to this file) is the generator: it measures the text with part="probe",
// picks text_size from a letter height and a maximum length, passes `ink`, and builds the
// 3D parts from the slab2d sections (fast). This file also works on its own in OpenSCAD
// (slower; text_size is then the raw OpenSCAD size and the name's position is estimated).

// Bundled fonts (OFL / Apache), registered so nothing needs installing.
use <fonts/Sniglet-ExtraBold.ttf>
use <fonts/LilitaOne-Regular.ttf>
use <fonts/TitanOne-Regular.ttf>
use <fonts/LuckiestGuy-Regular.ttf>
use <fonts/Chewy-Regular.ttf>
use <fonts/BowlbyOne-Regular.ttf>
use <fonts/BubblegumSans-Regular.ttf>

/* [Text] */
name = "AUBREY";
// Any font OpenSCAD can see, as "Family:style=Style". Bundled: Sniglet:style=ExtraBold,
// Lilita One, Titan One, Luckiest Guy, Chewy, Bowlby One, Bubblegum Sans
font = "Sniglet:style=ExtraBold";
// OpenSCAD text size. bag_tag.py derives it from letter_h (cap height) and max_len
text_size = 14.5;
// Letter spacing factor (OpenSCAD text spacing)
spacing = 1.0;
// "down" reads top to bottom (letter tops to the right, like the original); "up" bottom to top
text_dir = "down"; // [down, up]
// Removes base-colour walls and islands thinner than 2*weld (they can't print anyway)
weld = 0.35;
// [x0, x1, y0, y1] ink box of the name at text_size, baseline origin (set by bag_tag.py)
ink = [0, 0, 0, 0];

/* [Body] */
thickness = 6;
floor_t = 2.4;
// Base-colour rim around the inlay
border = 3.2;
// Rounds the outline and fills notches (closing radius)
smooth = 2.0;
// "follow": the rim follows the letters; "bar": a straight-sided strip under the name
outline_style = "follow"; // [follow, bar]

/* [Flower] */
flower = true;
petals = 5;
// Flower centre to petal tip
flower_r = 11.5;
petal_r = 5;
// Base-colour island in the middle of the flower
centre_r = 3.2;
// Angle of the first petal; -90 puts one petal pointing down at the name
flower_rot = -90;
// Base colour between the flower and the name
gap = 2.6;

/* [Keyring loop] */
loop = true;
loop_hole = 4.5;
loop_wall = 2.4;
loop_t = 3;

/* [Inlay] */
// Hidden bar joining the letters into one piece (prints face-down). false = loose letters, face-up
sprue = true;
sprue_w = 1.6;
sprue_h = 1.0;
// Bar centre above the baseline: inside the letters' bottom strokes, clear of their counters
sprue_lift = 1.1;
// Per side: the inlay is this much smaller than the pocket. 0 for a TPU inlay (it squeezes in)
clearance = 0.1;
// The inlay's first printed layer is inset by this much (no elephant foot)
relief = 0.25;
relief_h = 0.2;
// Inlay face above the rim (+ gives raised letters)
inlay_proud = 0;

/* [Output] */
part = "assembly"; // [assembly, base, inlay, plate, outline2d, pocket2d, inlay2d, sprue2d, probe, info]
// slab2d: which section ("base" | "inlay") and its index (see info)
slab_set = "base";
slab = 0;

$fn = 48;
eps = 0.01;

// ---------------------------------------------------------------- derived

has_ink = ink[1] > ink[0] && ink[3] > ink[2];
// Ink length and height of the name (estimated if bag_tag.py didn't measure it)
name_len = has_ink ? ink[1] - ink[0] : len(name) * text_size * 0.72;
name_h = has_ink ? ink[3] - ink[2] : text_size * 0.75;
// Baseline in the centred text frame (y = 0 is the middle of the ink)
baseline_y = has_ink ? -(ink[2] + ink[3]) / 2 : -name_h / 2;
// Y of the top of the name (first letter for "down")
name_top = flower ? -(flower_r + gap) : 0;

function petal_c(i) = (flower_r - petal_r) *
    [cos(flower_rot + i * 360 / petals), sin(flower_rot + i * 360 / petals)];

// Top of the rim at x = 0 (where the loop joins), before smoothing
function rim_top() = flower
    ? max(concat([flower_r - petal_r + border],
          [for (i = [0 : petals - 1]) let (c = petal_c(i), R = petal_r + border)
              abs(c[0]) < R ? c[1] + sqrt(R * R - c[0] * c[0]) : -1e9]))
    : name_top + border;

loop_cy = rim_top() + 1.0 + loop_hole / 2;
loop_r = loop_hole / 2 + loop_wall;

tag_top = loop ? loop_cy + loop_r : rim_top();
tag_bottom = name_top - name_len - border;
tag_w = max(flower ? 2 * (flower_r + border) : 0, name_h + 2 * border);

inlay_top = thickness + inlay_proud;
inlay_bot = sprue ? floor_t - sprue_h : floor_t;

function qsort(v) = len(v) <= 1 ? v : let (p = v[0])
    concat(qsort([for (x = v) if (x < p) x]), [for (x = v) if (x == p) x], qsort([for (x = v) if (x > p) x]));
function uniq(v) = [for (i = [0 : len(v) - 1]) if (i == 0 || v[i] > v[i - 1] + 1e-6) v[i]];
function pairs(v) = [for (i = [0 : len(v) - 2]) [v[i], v[i + 1]]];

// Horizontal sections: within each, the base / inlay is a straight prism
base_slabs = pairs(uniq(qsort([0, inlay_bot, floor_t,
                               loop ? min(max(loop_t, floor_t), thickness) : floor_t, thickness])));
inlay_slabs = sprue
    ? [[inlay_bot, floor_t, "sprue"], [floor_t, inlay_top - relief_h, "full"],
       [inlay_top - relief_h, inlay_top, "relief"]]
    : [[floor_t, floor_t + relief_h, "relief"], [floor_t + relief_h, inlay_top, "full"]];

// ---------------------------------------------------------------- 2D

// The name, unrotated, ink starting at x = 0 (or ending at x = 0 with at_end), centred on y = 0
module name_raw(at_end = false) {
    if (has_ink)
        translate([at_end ? -ink[1] : -ink[0], -(ink[2] + ink[3]) / 2])
            text(name, size = text_size, font = font, spacing = spacing,
                 halign = "left", valign = "baseline");
    else
        text(name, size = text_size, font = font, spacing = spacing,
             halign = at_end ? "right" : "left", valign = "center");
}

// Something drawn in the centred text frame, put in place: runs from name_top down
module in_name_frame(at_end = false) {
    translate([0, name_top])
        if (text_dir == "up") rotate(90) translate([at_end ? 0 : -name_len, 0]) children();
        else rotate(-90) children();
}

module name2d() {
    translate([0, name_top])
        if (text_dir == "up") rotate(90) name_raw(at_end = true);
        else rotate(-90) name_raw();
}

module flower2d() {
    circle(r = flower_r - petal_r);
    for (i = [0 : petals - 1]) translate(petal_c(i)) circle(r = petal_r);
}

module flower_pocket() difference() { flower2d(); circle(r = centre_r); }

// Walls and islands thinner than 2*weld are closed (they'd be gap fill or nothing at all)
module name_pocket() offset(r = -weld) offset(r = weld) name2d();

module pockets() {
    if (flower) flower_pocket();
    name_pocket();
}

// The groove/bar: a band just above the baseline, kept well inside the rim
module sprue2d() {
    if (sprue) intersection() {
        in_name_frame() translate([0.5, baseline_y + sprue_lift - sprue_w / 2])
            square([name_len - 1, sprue_w]);
        offset(r = border - 0.8) name_pocket();
    }
}

// Outline of the full-height body (no loop)
module body2d() {
    offset(r = -smooth) offset(r = smooth) offset(r = border) union() {
        if (flower) flower2d();
        name_pocket();
        if (outline_style == "bar")
            hull() name2d();
    }
}

module loop2d() {
    hull() {
        translate([0, loop_cy]) circle(r = loop_r);
        translate([0, loop_cy - loop_r - 2]) circle(r = loop_r);
    }
}

module hole2d() if (loop) translate([0, loop_cy]) circle(d = loop_hole);

module outline2d() difference() {
    union() { body2d(); if (loop) loop2d(); }
    hole2d();
}

module inlay2d() offset(delta = -clearance) pockets();

// Section of the base starting at height z0
module base_slab2d(z0) difference() {
    union() {
        body2d();
        if (loop && z0 < loop_t - eps) loop2d();
    }
    if (z0 >= floor_t - eps) pockets();
    if (sprue && z0 >= inlay_bot - eps && z0 < floor_t - eps) sprue2d();
    hole2d();
}

// Section of the inlay (in place in the base)
module inlay_slab2d(kind) {
    if (kind == "sprue") offset(delta = -clearance) sprue2d();
    else if (kind == "relief") offset(delta = -relief) inlay2d();
    else inlay2d();
}

module slab2d(set, k) {
    if (set == "base") base_slab2d(base_slabs[k][0]);
    else inlay_slab2d(inlay_slabs[k][2]);
}

// ---------------------------------------------------------------- 3D

module stack(slabs, set) {
    for (k = [0 : len(slabs) - 1]) let (z0 = slabs[k][0], z1 = slabs[k][1])
        if (z1 > z0 + 1e-6)
            translate([0, 0, z0 - (k > 0 ? eps : 0)])
                linear_extrude(z1 - z0 + (k > 0 ? eps : 0)) slab2d(set, k);
}

module base() stack(base_slabs, "base");

// The inlay where it sits in the base
module inlay_placed() stack(inlay_slabs, "inlay");

// The inlay as printed: with the sprue it's turned face-down (a rotation, so the letters read
// backwards from above, as they should when printed face-down)
module inlay() {
    if (sprue) translate([0, 0, inlay_top]) rotate([0, 180, 0]) inlay_placed();
    else translate([0, 0, -floor_t]) inlay_placed();
}

// ---------------------------------------------------------------- output

function join(v, i = 0) = i >= len(v) ? "" : str(i > 0 ? "," : "", v[i], join(v, i + 1));

module info() {
    echo(str("INFO {\"name\":\"", name, "\",\"text_size\":", text_size,
             ",\"name_len\":", name_len, ",\"name_h\":", name_h,
             ",\"length\":", tag_top - tag_bottom, ",\"width\":", tag_w,
             ",\"thickness\":", thickness, ",\"floor_t\":", floor_t,
             ",\"inlay_bot\":", inlay_bot, ",\"inlay_top\":", inlay_top,
             ",\"inlay_flip\":", sprue ? "true" : "false",
             ",\"base_slabs\":", base_slabs,
             ",\"inlay_slabs\":[", join([for (s = inlay_slabs) str("[", s[0], ",", s[1], ",\"", s[2], "\"]")]), "]",
             ",\"loop_cy\":", loop_cy, ",\"tag_top\":", tag_top, ",\"tag_bottom\":", tag_bottom, "}"));
}

if (part == "base") base();
else if (part == "inlay") inlay();
else if (part == "assembly") {
    color("gold") base();
    color("white") inlay_placed();
}
else if (part == "plate") {   // both parts as printed, side by side (preview only: print them separately)
    color("gold") base();
    color("white") translate([tag_w + 6, 0, 0]) inlay();
}
else if (part == "outline2d") outline2d();
else if (part == "pocket2d") pockets();
else if (part == "inlay2d") inlay2d();
else if (part == "sprue2d") sprue2d();
else if (part == "slab2d") slab2d(slab_set, slab);
else if (part == "probe") {   // the name at baseline origin, plus an "H" far above for the cap height
    text(name, size = text_size, font = font, spacing = spacing, halign = "left", valign = "baseline");
    translate([0, 1000]) text("H", size = text_size, font = font, halign = "left", valign = "baseline");
}
else if (part == "info") info();
