---
name: group-project
description: Run a student group project - set up a project with a tasks list, split work fairly by person and deadline, track progress, and write a short "WhatsApp summary" message the student can paste into the group chat (or send through the paired phone with approval). Use for group assignments, team presentations, "who's doing what", "summarise for the group".
tags: [student, group, project, tasks, whatsapp, teamwork]
version: 1
---

# Group project

Keep a student team organised with one shared source of truth and short, friendly updates.

## 1. Set it up
- Ask for: the assignment brief, due date (and any check-in dates), group members' names, and how the group talks (usually WhatsApp).
- Create a Blaze project for it (folder `group-<course>-<topic>`), and write `TASKS.md` there:
  - Header: course, title, due date, members, submission format.
  - Table: # · task · owner · due · status (todo / doing / done / blocked) · notes.
- Put the brief's summary and rules into the project brief (`update_brief`) so every later chat knows them.

## 2. Split the work
- Break the brief into 8-15 concrete tasks (research parts, writing sections, slides, code modules, editing, referencing, rehearsal, submission).
- Balance by effort, not task count; give each member one visible piece (for the presentation) and one supporting piece.
- Internal deadlines 3-4 days before the real one, final merge 2 days before, rehearsal 1 day before.
- Suggest the split; the student confirms or edits names before anything is shared.

## 3. Track
- When the student reports progress ("Mutale finished the intro"), update `TASKS.md`. Flag tasks overdue or blocked, and what unblocks them.
- Offer a Schedule reminder for each internal deadline (`schedule` add, `once YYYY-MM-DD HH:MM`).

## 4. WhatsApp summary
- Write a message that fits on one phone screen: greeting, 1-line status, each member's next task and date (with an emoji tick for done), one ask, the next meeting. Plain text, no Markdown tables, *bold* only with WhatsApp asterisks.
- Example shape:
  "Hi team 👋 Status: 6/12 tasks done, on track for Fri 20 Nov.\n✅ Chanda – intro\n⏳ Mutale – data section, by Tue\n⏳ Luke – slides 4-8, by Wed\nAsk: send sources to the doc by tonight 🙏\nCall: Thu 19:00"
- Show it for the student to copy. If a phone is paired over Blaze Link, offer to send it with `phone_ask` - and only after the student approves the exact text and the group name.

## Checklist
- [ ] Brief, due date, members and channel collected.
- [ ] Project + TASKS.md created; brief saved with update_brief.
- [ ] Work split fairly with internal deadlines; student approved names.
- [ ] Progress updates reflected in TASKS.md; reminders offered.
- [ ] WhatsApp summary fits one screen; sent only after explicit approval.
