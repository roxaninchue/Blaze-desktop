---
name: browser-game
description: Make a small playable game - arcade, puzzle, platformer, endless runner, quiz, card or board game - as one self-contained HTML file with canvas or DOM, keyboard, mouse and touch controls, sound and a score, that runs in Design and any browser. Use for "make a game", "build me a playable", clones of classic games and game jam prototypes.
tags: [games, html, canvas, javascript, interactive, design]
version: 1
---

# Browser game

Finish line: a game that is fun within ten seconds, playable start to game-over to restart in
Design, in one HTML file that works offline.

## 1. Shrink the idea to its core loop

- Write one sentence: the verb, the goal, the threat ("jump over cacti; survive; speed rises").
- Version one: one mechanic, one level or an endless loop, a score, a game-over and a restart.
  Extra modes and menus come after it's fun.
- Pick the medium: canvas 2D for action and physics; DOM/CSS grid for puzzles, cards, board games
  and quizzes (easier to style and to edit in Design); Three.js only when it truly needs 3D.

## 2. Structure (one file, `design/<game>.html` or `games/<game>/index.html`)

1. State in one object (`const G = {mode: "title"|"play"|"over", score, entities…}`), a fixed-step
   update (`dt` capped at 1/30 s) and a separate draw, driven by `requestAnimationFrame`.
2. Input: keyboard (arrows/WASD + space), mouse, and touch (on-screen buttons or swipe) so it plays on
   a phone preview too. Pause on blur. Prevent page scroll on arrow and space keys.
3. Feel: screen shake and a hit-pause of 50-80 ms on impacts, particles on scoring, easing on UI,
   coyote time and jump buffering for platformers, forgiving hitboxes (smaller than the sprite).
4. Sound: tiny WebAudio blips (an oscillator with a short envelope) for jump, hit and score; a mute
   button; start audio only after the first input.
5. Difficulty ramps with time or score; the first 10 seconds must be easy.
6. Save the high score in `localStorage`. Canvas scales to the window with crisp pixels for pixel art.
7. Comment each section (state, input, update, draw, audio, UI) so the user can tweak numbers; put
   tuning constants (speed, gravity, spawn rate) together at the top.

## 3. Test it

- Play it yourself headless: script inputs with a headless browser for 30 s, check no errors in the
  console, the score moves, game-over and restart work. Fix what you find, then hand it over.
- Check 60 fps on a modest laptop (no per-frame allocations in hot loops).

## Mixing with other skills

- Art: `pixel-art` sprites or `paper-sketch` doodles as the game's look.
- Music and effects: `music-audio` for a loop and SFX files.
- Publish: Design's ship to Cloudflare or GitHub Pages, or `git-pr-workflow` for a repo.

## Checklist

- [ ] Core loop is fun and readable in ten seconds; title, play, game over and restart all work.
- [ ] Keyboard, mouse and touch; pause on blur; mute button; high score saved.
- [ ] Tuning constants at the top; sections commented; tested headless with no console errors.
