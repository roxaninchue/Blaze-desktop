---
name: pixel-art
description: Make pixel art - game sprites, characters, tiles and tilesets, icons, emoji-style stickers and animated sprite sheets - on a real pixel grid with a limited palette, previewed in Design and exported as crisp scaled PNG, GIF or a sprite sheet. Use for "pixel art", "8-bit", "16-bit", "sprite", "tileset" or retro game asset requests.
tags: [pixel-art, sprites, games, retro, png, design]
version: 1
---

# Pixel art

Finish line: art on an exact pixel grid (16×16, 32×32, 64×64…) with a small palette, exported
nearest-neighbour scaled so every pixel stays a sharp square.

## 1. Settle the canvas and palette

- Size: icon or item 16×16; character 32×32 (or 16×24 for tiny RPG folks); portrait 64×64; tile 16×16
  or 32×32. Ask only if the target engine fixes a size.
- Palette: 4-16 colours. Pick a known one when it fits the vibe (PICO-8's 16, Game Boy's 4 greens,
  Endesga 32), or build a ramp per material: dark outline, shadow, base, light, highlight.
- Light from the top-left, always. Outline: dark coloured (not pure black) for a modern look; selective
  outlining (lighter where light hits) for polish.

## 2. Author it as data, not as freehand

1. Write the sprite as rows of palette indexes in a small script (Python or JS), e.g.
   `rows = ["....1111....", "...122221...", …]` with `PAL = {"1": "#2b2034", "2": "#e0a46a", ".": None}`.
   This keeps pixels exact and editable, and makes frames easy to copy and change.
2. Render with Pillow (`Image.new("RGBA", (w, h))`, `putpixel`) or as SVG `<rect>`s (one per run of
   pixels, `shape-rendering="crispEdges"`) for the Design preview at `design/<name>-pixels.html`:
   the SVG scaled up with `image-rendering: pixelated`, one `<g id>` per layer or frame.
3. Clean-up pass: no orphan single pixels, no "jaggies" (uneven stair steps on curves: use 1-1-2-2-3
   patterns), no pillow shading (shading that just follows the outline), anti-alias by hand only on large sprites.

## 3. Animation and sheets

- Walk cycle 4-6 frames, idle 2-4 (a one-pixel bob), attack 3-5. Keep the sprite's feet on the same row.
- Sprite sheet: frames in a row, equal cells, no padding unless the engine wants it; write a JSON
  with frame rects when the engine reads one (Phaser, Godot `SpriteFrames`, Unity slicing by cell size).
- GIF preview: Pillow `save(..., save_all=True, append_images=frames, duration=120, loop=0, disposal=2)`.

## 4. Export

- Scale by whole numbers with nearest neighbour only: `img.resize((w*8, h*8), Image.NEAREST)` or
  `magick in.png -filter point -resize 800% out.png`. Never smooth.
- Keep the 1× original next to the scaled one; engines want 1×, chat and social want 8-16×.

## Mixing with other skills

- A small game with these sprites: `browser-game`. Favicons or app icons from a sprite: `image-assets`.
- A pixel-art 3D voxel version: `three-d-design` with box parts per voxel layer.

## Checklist

- [ ] Exact grid size; palette of 16 colours or fewer; one light direction.
- [ ] No orphan pixels, jaggies or pillow shading.
- [ ] Exported 1× and nearest-neighbour scaled; sheet and frame data when animated.
