---
name: floor-plan
description: Draw 2D floor plans and layouts to scale - a room, flat, house, office, shop, garden, event or seating layout - as a measured SVG with walls, doors, windows, furniture and dimensions, previewed and edited in Design, with PDF and PNG export. Use for "floor plan", "room layout", "where should furniture go", "seating plan" or site layout requests.
tags: [floor-plan, layout, architecture, interior, svg, design]
version: 1
---

# Floor plan

Finish line: a plan drawn to scale, with dimensions that add up, which a builder, landlord or
friend can read, open in Design and exported to PDF for printing.

## 1. Get the measurements

- Use what the user gives (or a photo of a sketch with numbers). Missing sizes: use standard ones
  and label them "approx." (door 820-900 mm wide, single bed 900 × 1900, double 1400 × 1900, sofa
  2000 × 900, dining chair needs 600 mm width and 750 mm pull-out, walkway 900 mm, desk 1200 × 600).
- Units: metric (mm, labels in m) unless the user is in feet and inches; never mix.

## 2. Draw to scale in SVG

1. `design/<place>-plan.svg`; the viewBox is in millimetres (a 4.2 × 3.6 m room is
   `viewBox="-500 -500 5200 4600"` with margins). Keep a single unit so every number is a real size.
2. Layers as `<g id>` groups, bottom to top: `walls` (filled, 100-250 mm thick, dark), `openings`
   (gaps in walls; doors as a leaf line plus a quarter-circle swing; windows as three thin parallel
   lines), `fixtures` (kitchen, bath, stairs with arrow up/down), `furniture` (one `<g id>` per piece,
   thin outlines, light fill, a small label), `dimensions` (outside the walls, ticks at ends, the
   number centred), `labels` (room names and areas in m²), `north` arrow and a scale bar.
3. Dimension rules: overall sizes outside, room sizes inside, the parts of a wall add up to the whole.
   Compute areas, don't eyeball them.
4. Furniture moved in Design writes back to the file. Keep each piece's `transform="translate(x y) rotate(a)"`
   simple so moves stay readable.

## 3. Check

- Clearances: 900 mm walkways, doors that swing without hitting furniture, beds reachable from two
  sides where it matters, 1000 mm in front of kitchen counters. Point out anything that doesn't fit.
- Render to PNG and look; text readable at A4.

## 4. Export

- PDF to print at true scale: `rsvg-convert -f pdf -o plan.pdf plan.svg`. State the scale on the sheet (1:50 or 1:100 on A4/A3).
- Variants: one file per option (`plan-option-a.svg`…) when comparing layouts.

## Mixing with other skills

- Walk-through in 3D: turn the plan into `three-d-design` parts (walls as boxes, furniture as parts), same numbers.
- Seating plan for an event: this skill plus a guest list from `data-analysis`.
- Sketchy presentation look: `paper-sketch` filters on top of the plan's groups.

## Checklist

- [ ] One unit, true scale, dimensions that add up, areas computed.
- [ ] Walls, openings, furniture, dimensions and labels each in their own group; north arrow and scale bar.
- [ ] Clearances checked and problems pointed out; PDF exported at a stated scale.
