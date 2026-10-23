---
name: three-d-design
description: Design anything 3D (a product, furniture, a printable part, a building, a character) as a Three.js mockup in the Design tab that the user can orbit, pick and edit part by part, then build it in the program they named (Blender, FreeCAD, OpenSCAD, Fusion 360, Unity) from the same parts. Use for any 3D model, 3D design, CAD or 3D printing request.
tags: [3d, design, threejs, cad, blender, modeling, mockup]
version: 1
---

# 3D design: mockup first, then the real program

Every 3D request goes through the Design tab first. The mockup is the blueprint: one Three.js
page built from a JSON list of parts, which the user can orbit, click part by part and edit
(colour, size, position, rotation) with the edits written straight back to the file. Only when
the mockup is right do you build it in the program the user asked for, from the same numbers.

Starter: `assets/mockup.html` in this skill's folder (read it with `skill_file`). Copy it, keep its
structure, and replace the example parts.

## 1. Pin down what it is (ask only what you can't infer)

- **What and how big**: the object, its overall size and units (mm by default; m for buildings and rooms).
- **The parts** that make it: list them before writing anything. A stool is a seat, legs, a footrest; a
  phone stand is a base, an arm, a lip. Real proportions beat guesses: look up standard sizes (seat
  height 450 mm, desk 740 mm, door 2040 × 820 mm, M3 screw hole 3.2 mm) when the user gave none.
- **The target program**, if the first or the last message names one (Blender, FreeCAD, OpenSCAD,
  Fusion 360, SketchUp, Unity, Godot, Onshape, Rhino, Maya, Cinema 4D, a slicer for 3D printing).
  None named: the mockup (plus its .glb export) is the deliverable.

## 2. Build the mockup in Design

1. Copy the starter to `design/<thing>-3d.html` in the project folder (no project: the current folder).
2. Fill the `blaze-parts` JSON: `name`, `units`, `scale` (scene units per unit: 0.001 for mm, 1 for m),
   `nudge` (arrow-key step: 10 for mm objects, 0.1 for rooms in m), then one entry per part:
   - `id` kebab-case and unique, `label` in plain words.
   - `shape`: `box` (size [w, h, d]), `cylinder` (radius, height, optional radiusTop), `cone`, `sphere`,
     `torus` (radius, tube), `capsule`, `plane` (size [w, d]), `group` (a container), or `custom`.
   - `position` = the part's centre relative to its `parent` (or the world), `rotation` in degrees
     [x, y, z], `color` as hex, `material` (`matte`, `wood`, `metal`, `plastic`, `glass`).
   - Use `parent` + `group` for assemblies (legs, a drawer and its handle) so they move together.
3. Shapes the presets can't express go in `CUSTOM` (extrusions, lathes, tubes along curves) as one
   builder per part id. Keep their dimensions in the part's JSON entry and read them from `p`, so the
   numbers stay editable in Design.
4. Never hard-code a part's numbers in the script. The JSON is the single source of truth; Design
   edits it, and step 4 reads it.
5. **Annotate the code**: every block starts with a `// ---- N. What this block does ----` comment and
   a line on why, like the starter. When you add custom builders, comment what each one makes and
   which JSON keys drive it.
6. Keep the contract the Design tab relies on: each part's root `Object3D` has `userData.part = id`,
   and the page sets `window.__blazeScene = { THREE, scene, camera, renderer, controls, parts, spec }`.
7. Save. The app sees a 3D mockup and opens it in Design. Say in one or two lines what you built,
   the overall size, and how to edit: click a part, then Adjust (colour, scale, turn), arrow keys to
   move it, or Change to describe anything else.

## 3. Iterate with the user

- A `[Design edit]` naming `Part: <id>` changes that part only: edit its JSON entry, and its custom
  builder only when the shape itself changes. Keep every other part identical.
- Bigger changes ("make it foldable", "add a drawer") add or replace parts in the JSON. Keep ids stable
  so earlier versions stay comparable in Design's history.
- Check proportions after each change: parts that should touch must touch (a leg's top at the seat's
  underside), nothing floats or intersects unless it should.

## 4. Build it in the target program (only after the mockup)

Ask "Happy with the mockup? I'll build it in <program> next" unless the user already said to go
straight on. Then generate the model from the same `blaze-parts` JSON, so both match exactly:

| Program | How |
| --- | --- |
| Blender | A Python script (`bpy`) that creates each part as a named object (same id), sets dimensions, location, rotation (degrees to radians) and a material per colour, parents groups, and saves `<thing>.blend`. Run it with `blender --background --python build.py` when Blender is installed. |
| FreeCAD | A Python script using `Part` (makeBox, makeCylinder, makeCone, makeSphere, makeTorus) with placements, saved as `.FCStd`; run with `freecadcmd build.py`. Export STEP for other CAD tools. |
| OpenSCAD | A `.scad` file: one module per part, `translate` / `rotate` / `color`, a top-level assembly; dimensions as named variables at the top. Render STL with `openscad -o <thing>.stl <thing>.scad` for printing. |
| Fusion 360, Onshape, SolidWorks, Rhino | Export STEP from FreeCAD or OpenSCAD (via FreeCAD), plus the parts table as a dimensioned blueprint for rebuilding parametrically. Fusion 360 also takes a Python add-in script built from the JSON. |
| SketchUp, Maya, Cinema 4D, 3ds Max | The mockup's .glb (Download .glb button, or the same scene exported with GLTFExporter from a script), plus the parts table. |
| Unity, Godot, Unreal | The .glb straight into the project's assets folder; one node per part, named by id. |
| 3D printing | OpenSCAD to STL (millimetres, solid, no zero-thickness faces), then the user's slicer. |

Check what is installed first (the PC profile lists tools). If the program isn't installed, write the
script or file anyway, say how to run it, and offer to install the program.

After building: open the file or output folder, say what was made and where, and note anything that
differs from the mockup (for example, fillets added in CAD that the mockup only suggests).

## Checklist before you hand it over

- [ ] Every part is in the blaze-parts JSON with a unique id, a label and real dimensions.
- [ ] No part numbers are hard-coded in the script; custom builders read them from the part.
- [ ] Each code block has its annotation comment.
- [ ] Parts that should touch do, nothing floats, and the overall size matches the brief.
- [ ] `window.__blazeScene` is set and each part root carries `userData.part`.
- [ ] If a program was named: the mockup came first, and the program's file is built from the same JSON.

## Don'ts

- Don't skip the mockup, even when the user named a program: Design first, then the program.
- Don't use external model downloads or textures from the web unless the user asks; build from primitives.
- Don't invent brand names, measurements presented as official specs, or certifications.
