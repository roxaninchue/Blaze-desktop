---
name: classroom-sync
description: Sync Google Classroom into Blaze - list the student's courses and upcoming assignments, then add each one to Schedule with a reminder 24 hours before it is due, without duplicates. Use for "sync my Classroom", "what assignments are due", "remind me of my coursework" or a weekly assignment check.
tags: [student, google-classroom, assignments, schedule, reminders, sync]
version: 1
---

# Classroom sync

Every upcoming Classroom assignment becomes a one-time Blaze reminder 24 hours before it's due.

## 0. Needs
- The Google connector (Accounts › Google). Classroom permission is asked the first time a Classroom tool runs: a Grant access button appears in the chat. If Google isn't connected, Blaze shows a Connect button; wait for it.
- Tools: `classroom_courses`, `classroom_coursework` (connector tools; turn them on with find_tools "classroom" if they aren't loaded), and `schedule`.

## 1. Read
- `classroom_courses` → active courses.
- `classroom_coursework` (days 30 by default; 60 near the end of a semester) → each line has the local due time, course, title, points, submission state and ids.
- Skip work that is already turned in or returned; keep missing and late items and flag them first.

## 2. Show
- A short list grouped by day: `Mon 16 Nov 23:59 · NET103 · Subnetting lab (20 pts)`. Late or missing work on top with a ⚠.
- Ask once: "Add reminders 24 h before each? (n items)". A standing yes from memory (`recall` "classroom reminders") counts.

## 3. Schedule
- For each item due more than 24 h from now: `schedule` action add, name `cls-<courseId>-<workId>` (stable, so re-syncing replaces instead of duplicating), when `once <due minus 24h, local, YYYY-MM-DD HH:MM>`, prompt: "Classroom reminder: <course> – <title> is due <due time>. Link: <link>. Notify the user and offer help (study-planner or tutor)."
- Due in under 24 h: add a reminder 2 hours before instead, and tell the student now.
- Before adding, `schedule` list and skip items whose `cls-…` name already exists with the same time; if the due date changed, remove and re-add.

## 4. Report and remember
- One line: "Added 5 reminders, 2 already there, 1 due tomorrow (reminder at 21:59)."
- Offer a weekly sync: `schedule` add name `classroom-weekly`, when `weekly sun 18:00`, prompt "Run classroom-sync and tell me what's new".
- `remember` the student's choice (reminders yes/no, weekly sync yes/no).

## Checklist
- [ ] Google connected; Classroom permission granted when asked.
- [ ] Courses and upcoming work listed; turned-in work skipped; late work flagged.
- [ ] Student agreed (or a remembered standing yes).
- [ ] One `once` reminder per item, 24 h before (2 h if due sooner), stable `cls-` names, no duplicates.
- [ ] Short report; weekly sync offered; choice remembered.
