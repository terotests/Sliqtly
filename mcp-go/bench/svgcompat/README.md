# SVG compatibility: the preview against the player

How faithfully `render_slide` / `render_overview` (`svgraster.go`: resvg
0.48 as WebAssembly) draw an SVG picture compared with the player, which
draws it in the browser (`web/picture.js`: the SVG in an `<img>`, drawn
once onto a canvas).

## Suite

The **W3C SVG 1.1 (Second Edition) test suite**, as web-platform-tests
keeps it (`svg/import`, 513 tests; wpt
`da1f6d20caf40b4003ae288dda8c97f252dd9264`, 2026-10-05). The W3C's own copy
on w3.org is not reachable from the build machines; the files are the same.

## Method

- **Player**: Chromium (Playwright's 1194 build) given each SVG exactly as
  the player gives it: root sized by `web/picture.js svgSizedTo` to
  480×360, shown from a Blob in an `<img>` (so, like the player, it loads
  nothing from outside the file), drawn onto a canvas (`chrome.mjs`).
- **Preview**: `drawSvg` at the same size (`svgcompat_test.go`).
- Both see only the editor's faces: Chromium through `fonts.conf` (every
  family is Open Sans), the preview as it always does. A difference in
  text is then the renderer's, not the font's.
- Both are laid over white and compared pixel by pixel. A pixel differs
  when no pixel of the other picture within one pixel of it is within 48
  of it in every channel, both ways: edges and glyphs a pixel apart (two
  rasterisers' rounding) do not count; a wrong colour or a missing shape
  does.
- **same**: at most 1 % of pixels differ. **close**: 1–5 %; looked at by
  eye, these are the same picture with text and edges a little apart.
  **differs**: over 5 %; each was looked at (below).

`sh run.sh` runs it all again and rewrites `results-w3c-svg11.json`.

## Results

| chapter | tests | same | close | differs | player shows nothing |
| --- | ---: | ---: | ---: | ---: | ---: |
| animate | 78 | 27 | 46 | 5 | 0 |
| color | 6 | 5 | 0 | 1 | 0 |
| conform | 2 | 1 | 0 | 1 | 0 |
| coords | 32 | 24 | 8 | 0 | 0 |
| extend | 1 | 1 | 0 | 0 | 0 |
| filters | 43 | 26 | 12 | 5 | 0 |
| fonts | 17 | 11 | 6 | 0 | 0 |
| imp | 1 | 1 | 0 | 0 | 0 |
| interact | 24 | 14 | 10 | 0 | 0 |
| linking | 12 | 9 | 3 | 0 | 0 |
| masking | 19 | 13 | 6 | 0 | 0 |
| metadata | 1 | 1 | 0 | 0 | 0 |
| painting | 23 | 15 | 8 | 0 | 0 |
| paths | 21 | 19 | 2 | 0 | 0 |
| pservers | 33 | 23 | 10 | 0 | 0 |
| render | 8 | 7 | 1 | 0 | 0 |
| script | 6 | 3 | 3 | 0 | 0 |
| shapes | 21 | 21 | 0 | 0 | 0 |
| struct | 72 | 57 | 12 | 3 | 0 |
| styling | 18 | 14 | 3 | 1 | 0 |
| svgdom | 1 | 1 | 0 | 0 | 0 |
| text | 59 | 16 | 36 | 6 | 1 |
| types | 15 | 9 | 4 | 2 | 0 |
| **all** | **513** | **318** (62.0 %) | **170** (33.1 %) | **24** (4.7 %) | **1** |
**95 % of the suite draws the same in the preview as in the player**
(same or close). The tests are 480×360; a slide preview draws a picture
up to 1920 px on its longer side.

## The 24 that differ, and why

| what | tests | the preview | the player (Chromium) |
| --- | --- | --- | --- |
| SMIL animation | animate-elem-02, -35, -38, -85, animate-pservers-grad-01 | the picture before any animation | the animation's state when the picture is drawn: an animated SVG is a snapshot in the player too, at a moment nobody chooses |
| Right-to-left text, bidi | text-intro-02, -05, -09, -10 | Arabic missing (no Arabic face among the editor's faces), Hebrew not reordered | drawn, reordered |
| `textLength` / `lengthAdjust` | text-text-01 | lines squeezed differently | |
| CSS system colours (`Window`, `ButtonFace` …) | color-prop-04 | black | the system's colours |
| CSS selectors | styling-css-10 | two selectors not matched | matched |
| Attribute value parsing | types-basic-02 | invalid values rejected by the test's rules | accepted |
| `BackgroundImage` / `BackgroundAlpha` filter inputs | filters-overview-01, -02, -03 | drawn (SVG 1.1) | empty (no browser implements them) |
| `feDisplacementMap`, `feTile` | filters-displace-02, filters-tile-01 | a different result / tiles a few pixels off | |
| `requiredFeatures`, `systemLanguage` | struct-cond-03, struct-cond-overview-02 | SVG Tiny branch chosen | the other branch |
| Pictures that do not load | struct-image-12, conform-viewers-02 | nothing | a broken-picture icon |
| DOM tests | text-dom-01, types-dom-06 | | scripts do not run in either; layout differs slightly |

And one the player shows as **nothing**: `text-tref-02` has a
`<foreignObject>`, which taints the canvas, so the player gets no PNG. The
report's ⚠ for a `<foreignObject>` says so (`rgr/SvgCheck.rgr`).

For slide backgrounds and drawings the gaps that matter are animation
(the player shows some moment of it), right-to-left text, and
`<foreignObject>`; the report already warns about text and filters, and
the guide says to draw words as paths.
