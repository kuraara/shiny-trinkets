# Photo Stitch

Join photos into one image: vertical (default), horizontal, or a grid of N columns
(rows automatic or fixed). Sizing is either **as they are** (no scaling or cropping, gaps
filled with the background) or **same shape** (crop and scale every photo to one aspect
ratio). Add photos by picking, dragging in, or pasting. Everything runs in the browser; nothing
is uploaded.

- **Borders and spacing**: a border around each photo (width and colour), the gap between
  photos, an outer margin, and the background colour (or transparent PNG). A toggle sets them
  in **pixels** (of the saved image, so 20 px stays 20 px at any size) or in **% of a typical
  photo's shorter side** (so they grow and shrink with the photos). Switching converts the
  current values.
- **File**: full resolution or a longest side of 4096 / 3000 / 2048 / 1600 / 1080 / 800 px or a
  custom size (photos are scaled down to fit; never enlarged), PNG or JPEG with a quality slider.

Very large results are scaled down to what the browser can save (iPhones cap a canvas at
about 16.7 megapixels); the page says when that happens.

Static files served by nginx. Stack `photo-stitch` on the PC, reached through the gadget
registry on port 8101. `test/e2e.js` checks every layout in headless Chromium.
