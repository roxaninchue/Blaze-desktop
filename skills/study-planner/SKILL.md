---
name: study-planner
description: Build a day-by-day study plan from an exam date and a list of topics, put each session in Schedule with a reminder, and re-plan when the student misses a day. Use for "help me study for my exams", "make a revision timetable", "I have finals on the 16th" or "I missed yesterday's study".
tags: [student, study, exams, planning, schedule, revision]
version: 1
---

# Study planner

Goal: a realistic plan the student actually follows, saved where Blaze can remind them, that bends instead of breaking when a day is missed.

## 1. Gather (ask only what memory and Classroom can't answer)
- Exams: course, date and time of each (check `recall` and, if Google is connected, `classroom_coursework` / `gcal_events` first).
- Topics per course, ideally from the syllabus or lecture list. If the student has files (outline PDF, slides), read them and list the topics yourself, then confirm.
- Hours a day they can really study, and fixed blocks to avoid (classes, meals, work, church, sleep). Ask once, in one question with a suggested default ("about 3 hours after 18:30?").
- How confident they are per topic: weak / OK / strong. Weak topics get more and earlier sessions.

## 2. Build the plan
- Work backwards from each exam. The last day before an exam is light review and practice questions only.
- Sessions of 45-50 minutes with a 10-minute break; at most 4 sessions a day unless the student asks for more.
- Interleave two courses a day rather than one course all day. Put weak topics early (time to revisit) and again 2-3 days later (spacing).
- Every topic gets: learn → practise (past papers, problems) → recall (flashcards via the tutor skill, or explain it aloud).
- Leave one buffer slot every 3-4 days for catch-up. Keep Sunday (or their chosen rest day) lighter.
- When exams overlap, alternate days and give the earlier exam priority in the final 3 days.

## 3. Show it, then save it
- Show the plan as a compact table: date · time · course · topic · task. One line per session, no essays.
- After the student says yes, save each day's first session as a Schedule item with `schedule` (action add, when `once YYYY-MM-DD HH:MM`, a Blaze prompt like "Study reminder: NET103 subnetting, 2 sessions, practise past paper Q3-5"). Name items `study-<course>-<date>`.
- Also `remember` the exam dates and the plan's file path so later chats can find it. Write the full plan to `study-plan.md` in the project folder.

## 4. Re-plan on a missed day
- When the student says they missed a session (or a reminder log shows nothing was done), never scold. Move the missed topics into the next buffer slot; if none is left before the exam, drop the lowest-weight strong topic to a 20-minute skim.
- Update `study-plan.md`, remove the old Schedule items (`schedule` remove) and add the new ones. Say in two lines what moved.

## Low-data mode
Keep the plan text-only; no images or downloads.

## Checklist
- [ ] Exam dates confirmed (from Classroom, calendar, memory or the student).
- [ ] Topics listed per course with a confidence rating.
- [ ] Plan respects their real hours and fixed blocks; buffer slots included.
- [ ] Shown as a short table and approved before anything is scheduled.
- [ ] Schedule items added (`once` reminders) and `study-plan.md` written.
- [ ] Missed days re-planned without guilt, plan file and reminders updated.
