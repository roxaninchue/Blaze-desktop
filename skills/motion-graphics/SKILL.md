---
name: motion-graphics
description: Make short animations and motion graphics - animated logos, loaders, explainer loops, kinetic text, animated stickers, social clips and UI micro-interactions - with CSS, SVG or canvas previewed live in Design, then exported to MP4, GIF, WebM or a Lottie-style JSON when needed. Use for "animate", "animation", "GIF", "loop", "motion" or short video graphic requests.
tags: [animation, motion, gif, video, css, svg]
version: 1
---

# Motion graphics

Finish line: a smooth animation running in Design, and the exported file the user asked for at the
right size, length and weight.

## 1. Brief

- Where it plays sets the format: website or app → CSS/SVG in the page (no video); chat or social →
  MP4 (or GIF if they say GIF); Slack/Discord sticker → GIF or APNG under the size limit; After
  Effects or app teams → Lottie JSON.
- Length: loaders 1-2 s loops; logo reveals 2-3 s; social clips 5-15 s. Sizes: 1080×1080 or 1080×1920
  social, 512×512 stickers, 1920×1080 video.

## 2. Build it as a page

1. `design/<name>-motion.html`, one stage element at the target size, centred.
2. Animate with CSS keyframes or the Web Animations API for simple timelines; SVG `stroke-dashoffset`
   for line drawing; canvas with `requestAnimationFrame` for particles. Keep everything in one timeline
   driven by a single `--t` (0 to 1) or one master duration so export can step through it.
3. Motion that feels good: ease-out for entering, ease-in for leaving, overshoot (`cubic-bezier(.34,1.56,.64,1)`)
   for playful pops, 150-400 ms for UI, stagger children by 40-80 ms. Animate transform and opacity only.
4. Loops must match: the last frame equals the first. Respect `prefers-reduced-motion` on websites.
5. Each animated piece is its own element with an id so Design can pick and adjust it.

## 3. Export

- Frames: expose `window.seek(t)` that sets the timeline to time t (pause all animations, set
  `currentTime`), then capture with a headless browser (Playwright: loop t over duration × fps,
  `page.screenshot({path: f"frames/{i:04}.png"})`). Use 30 fps (24 for a filmic look).
- MP4: `ffmpeg -framerate 30 -i frames/%04d.png -c:v libx264 -pix_fmt yuv420p -crf 18 -movflags +faststart out.mp4`.
- GIF (good colours): `ffmpeg -i out.mp4 -vf "fps=20,scale=600:-1:flags=lanczos,split[a][b];[a]palettegen[p];[b][p]paletteuse" out.gif`.
- WebM with alpha: `ffmpeg -framerate 30 -i frames/%04d.png -c:v libvpx-vp9 -pix_fmt yuva420p out.webm`.
- Lottie: only for vector shape animation; build with a Lottie library or the user's tool, and say so honestly if a hand-written JSON is not practical.
- Check weight: GIF stickers often must be under 500 KB; cut fps, size or colours until they fit.

## Mixing with other skills

- Animate a sketch: draw with `paper-sketch`, then reveal strokes with `stroke-dashoffset` here.
- Animate a pixel sprite: frames from `pixel-art`, timing and export from here.
- Turntable of a 3D mockup: `three-d-design` scene, rotate the camera, capture frames here.

## Checklist

- [ ] Right format, size and length for where it plays; loops seamlessly.
- [ ] Easing and stagger feel deliberate; only transform and opacity animated in pages.
- [ ] Exported, played back once, and under any size limit.
