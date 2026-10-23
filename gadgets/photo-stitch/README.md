# Photo Stitch

Join photos into one image: vertical (default), horizontal, or a grid of N columns
(rows automatic or fixed). Sizing is either **as they are** (no scaling or cropping, gaps
filled with the background) or **same shape** (crop and scale every photo to one aspect
ratio). Add photos by picking, dragging in, or pasting. Gap, background colour, transparent PNG
or JPEG. Everything runs in the browser; nothing is uploaded.

Very large results are scaled down to what the browser can save (iPhones cap a canvas at
about 16.7 megapixels); the page says when that happens.

Static files served by nginx. Stack `photo-stitch` on the PC, reached through the gadget
registry on port 8101. `test/e2e.js` checks every layout in headless Chromium.
