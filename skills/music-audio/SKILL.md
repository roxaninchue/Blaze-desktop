---
name: music-audio
description: Make music and sound with code - melodies, chord progressions, beats, jingles, ringtones, game sound effects and ambient loops - as MIDI, WAV or MP3 (and a playable page), plus cleaning, trimming and converting audio files. Use for "make a song", "compose a melody", "beat", "jingle", "sound effect", "ringtone" or audio editing requests.
tags: [music, audio, midi, sound-effects, ffmpeg, creative]
version: 1
---

# Music and audio

Finish line: a file the user can play right now (MP3 or WAV), the editable source (MIDI or the
script), and a page in Design to listen and tweak when that helps.

## 1. Brief in musical terms

- Mood, tempo (BPM), key and length: "upbeat, 120 BPM, C major, 15 s loop". Infer from the request
  (lullaby → slow, major; boss fight → fast, minor) and say what you chose.
- Use: ringtone (20-30 s, loud and clear, M4R for iPhone), game loop (seamless, 8-16 bars), jingle
  (3-6 s), SFX (under 1 s), background bed (quiet, no strong melody).

## 2. Compose with theory, not randomness

1. Chords first: a common progression in the key (I-V-vi-IV pop, i-VI-III-VII epic, ii-V-I jazz,
   12-bar blues). Then a melody from chord tones on strong beats and passing notes between, mostly
   steps with a few leaps, phrases of 2 or 4 bars that answer each other.
2. Rhythm: kick on 1 and 3, snare on 2 and 4, hats on eighths for pop; vary every 4th bar.
3. Write it as data in Python with `mido` or `midiutil` (notes as MIDI numbers, beats as floats), one
   track per part, and save `out/<name>.mid`.

## 3. Render to audio

- With a SoundFont: `fluidsynth -ni <soundfont.sf2> out.mid -F out.wav -r 44100`, then
  `ffmpeg -i out.wav -b:a 192k out.mp3`. No synth installed: offer `install_tool fluidsynth` (and a
  free General MIDI SoundFont), or synthesize simple tones with numpy (sine/square/saw plus an ADSR envelope) into a WAV.
- SFX by synthesis: jump = square wave rising pitch 80 ms; coin = two quick high notes; hit = noise burst
  with fast decay; explosion = low-passed noise, 600 ms decay.
- A listen page: `design/<name>-audio.html` with WebAudio (or Tone.js from a CDN) playing the same
  notes, play/stop, a tempo slider, and the note data in one commented array so Design edits stay simple.

## 4. Edit existing audio (ffmpeg)

- Trim: `ffmpeg -ss 00:00:05 -to 00:00:35 -i in.mp3 -c copy out.mp3`. Fade: `-af "afade=t=in:d=1,afade=t=out:st=29:d=1"`.
- Loudness for sharing: `-af loudnorm=I=-14:TP=-1:LRA=11`. Convert: `ffmpeg -i in.wav out.mp3`; iPhone ringtone: AAC in `.m4r`, under 30 s.
- Noise reduction: `-af afftdn` (gentle); speed without pitch change: `-af atempo=1.1`.

## Mixing with other skills

- Game audio: loops and SFX here, wired up in `browser-game`.
- Soundtrack for an animation: `motion-graphics` export, then mux: `ffmpeg -i v.mp4 -i m.mp3 -shortest -c:v copy out.mp4`.

## Checklist

- [ ] Tempo, key, mood and length chosen and stated; loops seamless where needed.
- [ ] Playable MP3 or WAV plus the editable MIDI or script; listened back (duration and level checked with ffprobe).
- [ ] No copyrighted melodies reproduced; anything "in the style of" stays original.
