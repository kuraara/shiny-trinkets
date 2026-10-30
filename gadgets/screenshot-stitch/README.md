# Screenshot Stitch

Screenshots taken as you scroll (a long chat, an article, a receipt) joined into one tall image,
with the text lined up. Everything happens in the browser; nothing is uploaded.

Through the gadget registry on port 8105 (http or https; on an iPhone use https so **Save to
Photos…** appears).

## Using it

1. Take screenshots top to bottom, each sharing a few lines with the one before.
2. Add them (choose, paste or drop) in that order. Each one says whether it lined up with the one
   before and how much they share; if it didn't, check the order or set the overlap by hand (− +).
3. The bars that stay put (status bar, title, tabs, search or typing box) appear once: the top
   one from the first screenshot, the bottom one from the last; you can leave either off.
   They're found automatically when they look the same in every screenshot. When content shows
   through them (iOS 26's see-through bars and floating search field), choose **Mark them** and
   drag the two lines to where the top bar ends and the bottom bar starts; that much is cut from
   every screenshot in between, and the matching ignores it.
4. Download, or **Save to Photos…** on an iPhone. Too tall for the browser to save in one go
   (iPhones stop at 16.7 million pixels, about 12,700 px tall at full width)? Split it into
   parts, cut between lines, or save one smaller image.

## How it lines them up

[`web/stitch.js`](web/stitch.js), plain JavaScript with no browser APIs, so it's unit-tested in Node:

- Every screenshot is scaled to the first one's width, then each row of pixels is summarised as
  brightness and edge strength in 64 strips across it. That survives JPEG noise and screenshots
  saved at different sizes, but still tells one line of text from the next.
- **Fixed bars**: rows at the top (and bottom) that are the same in two screenshots, allowing a
  few strips to differ (the clock, a spinner) and two pixels of misalignment. Or marked by hand.
- **Overlap**: every possible shift of the next screenshot against the last is scored on the rows
  just under its top bar, weighted towards rows with text (blank rows match anything); the best
  few are then checked row by row over the whole overlap.
- **Seam**: the switch from one screenshot to the next happens in a blank gap between lines near
  the middle of the overlap, so it doesn't show.

## Tests

    node --test test/stitch.test.mjs                 # the matching, on made-up screenshots
    node test/e2e.js https://<pc>:8105 /tmp/shots     # in Chromium at phone and desktop size (build box)
