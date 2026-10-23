---
name: lecture-notes
description: Turn a lecture recording or rough class notes into clean study notes - transcribe audio on this PC with Whisper, then write structured notes, a short summary, key terms with definitions and likely exam questions. Use for "transcribe my lecture", "make notes from this recording", "clean up my class notes".
tags: [student, lecture, notes, transcription, whisper, summary]
version: 1
---

# Lecture notes

From audio or messy notes to something worth revising from, without inventing anything the lecturer didn't say.

## 1. Get the text
- Audio or video file (m4a, mp3, wav, mp4): transcribe it on this PC with Blaze's local Whisper speech-to-text (the same engine as voice input; free and offline). If the file is long (> 30 min), transcribe in chunks and keep timestamps every few minutes. If Whisper isn't set up, say what to install and offer to do it with `install_tool`.
- Low-data mode: never upload audio to an online service; local Whisper only.
- Photos of the board or handwritten notes: read them with the vision tools if available, otherwise ask the student to type the key lines.
- Pasted or typed notes: use them as they are.
- Save the raw transcript next to the recording as `<name>.transcript.txt` so nothing is lost.

## 2. Write the notes (`<name>.notes.md`)
Structure:
1. **Title** - course code, lecture number/topic, date.
2. **Summary** - 5 bullet points, each one sentence.
3. **Notes** - headings that follow the lecture's order; short bullets; formulas, commands and code in code blocks; examples the lecturer gave kept as examples.
4. **Key terms** - table: term · plain definition (from the lecture, not a dictionary).
5. **Likely exam questions** - 3-6 questions the lecturer hinted at ("this will come up", repeated points, worked examples).
6. **Follow-ups** - things that were unclear in the audio, readings or deadlines mentioned (with the timestamp).

Rules: keep the lecturer's numbers and names exactly; mark unclear audio as `[unclear 12:40]` instead of guessing; no filler, no "in this lecture we will".

## 3. Offer the next step
- Offer to turn the key terms and exam questions into flashcards (tutor skill → `flashcards_add`, deck = course code).
- If a deadline or test date was mentioned, offer a Schedule reminder.

## Checklist
- [ ] Transcript saved (local Whisper; nothing uploaded in low-data mode).
- [ ] Notes follow the six sections, in the lecture's order.
- [ ] Numbers, names and code copied exactly; unclear parts marked with timestamps.
- [ ] Deadlines and readings pulled into Follow-ups.
- [ ] Flashcards and reminders offered.
