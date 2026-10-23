---
name: paper-sketch
description: Draw a 2D doodle, sketch or hand-drawn illustration that looks like pencil, pen or marker on real paper (notebook, sketchbook, sticky note, napkin, graph paper) as a self-contained SVG the Design tab previews and edits, with PNG export. Use for doodles, sketches, hand-drawn notes, storyboards and "draw me" requests.
tags: [drawing, sketch, doodle, svg, illustration, design]
version: 1
---

# Paper sketch

Finish line: one SVG at `design/<thing>-sketch.svg` that reads as a real drawing on real paper,
open in Design, plus a PNG when the user wants to share it.

Starter: `assets/sketch.svg` (read it with `skill_file`). It holds the paper, the pencil and ink
filters, hatching patterns and an example mug. Copy it and replace the doodle groups.

## 1. Decide the look (infer, ask only if it changes everything)

- **Paper**: notebook (blue rules + red margin, the starter), plain sketchbook (delete `#rules`),
  graph paper (a 20 px grid pattern in pale blue), sticky note (square, #fff59d, slight rotation and a
  shadow), napkin (off-white, no rules, soft folds as faint lines), kraft (#c9a878 paper, white gel pen).
- **Tool**: pencil (`#pencil` filter, graphite #33312e, hatching for shade), ballpoint (`#ink`,
  #1f3a93, thin 2 px), fineliner (`#ink`, black, even 2.5 px), marker (`#ink`, 6-10 px, opacity .85),
  highlighter accents (one colour, opacity .35-.45, slightly off the lines).
- **Size**: landscape 1100 × 850 by default; portrait 850 × 1100; square 1000 × 1000 for social.

## 2. Draw it so it looks drawn

1. Plan the composition on the rules: subject slightly off-centre, a caption on a rule line, room to breathe.
2. Draw **clean** shapes (paths, ellipses, simple curves) and let the filters add the wobble. Messy
   hand-jittered paths plus the filter look like a broken plotter.
3. Hand-drawn tells, used sparingly:
   - main outlines drawn twice: a second, thinner path offset by 2-4 px at opacity .6;
   - lines that overshoot corners a little, round caps and joins, gaps where lines don't quite meet;
   - shading with the `#hatch` pattern, darker areas with `#crosshatch` on top, a hatched cast shadow;
   - one accent colour at most, plus the paper's own colours.
4. Lettering: the starter's hand-style font stack (Segoe Print, Bradley Hand, Comic Neue…), rotated
   1-4°, sitting on a rule; underline with a wobbly path, arrows as short curved strokes with a `<`.
5. Group every part as `<g id="kebab-name">` (subject, shading, accents, caption) so Design can pick,
   recolour, retype and move each one. Comment each group with what it is.
6. Several frames (storyboard, comic strip, step-by-step how-to): one sheet, panels drawn as wobbly
   boxes, numbered in the corner, one group per panel.

## 3. Check and export

- Render to PNG and look at it before you say it's done:
  `rsvg-convert -w 2200 design/x-sketch.svg -o design/x-sketch.png` (or `magick -density 192 x.svg x.png`,
  or `inkscape x.svg -o x.png -w 2200`). No renderer: open the SVG in Design and judge it there.
- Look for: lines too faint (lower the pencil mask's grain), filters cut off at the edges (widen the
  filter region), text overflowing its line, too many colours.
- Share-ready: PNG at 2× width. Printable: the SVG, or a PDF via `rsvg-convert -f pdf`.

## Mixing with other skills

- A sketched UI wireframe: this skill's paper and pens plus `web-design-*` layout rules.
- A sketched diagram or mind map: `diagrams` for the structure, this skill for the look.
- A sketched slide or poster: draw here, then place the SVG with `slide-deck` or `print-design`.

## Checklist

- [ ] Reads as a drawing on paper at a glance (paper texture, wobble, hatching), not as clip art.
- [ ] One accent colour at most; lettering on a line, slightly rotated.
- [ ] Every part is its own commented `<g id>`.
- [ ] Rendered and checked as PNG; PNG exported when the user wants to share it.
