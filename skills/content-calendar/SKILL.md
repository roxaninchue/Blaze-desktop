---
name: content-calendar
description: Plan and make a week of social media posts for a store or brand - post ideas, captions, hashtags and visuals designed in the Design studio with the brand kit - and save each post as a Schedule item. Use for "plan my posts this week", "content calendar for HomeGlow", "what should I post on TikTok/Instagram".
tags: [marketing, social-media, content, calendar, design, schedule]
version: 1
---

# Content calendar

A week of on-brand posts the seller can actually publish, each with its visual and a reminder.

## 1. Brief (from memory first)
- Store, niche and audience (`recall` the store, e.g. HomeGlow: home lighting / decor, dropshipping on Shopify), platforms (Instagram, TikTok, Facebook, WhatsApp Status), posts per week (default 5), and this week's goals (launch, sale, restock, reviews).
- Products to push: ask for 2-4, or read them from the store (product-listing output, Shopify admin export, or the storefront).
- Brand kit: the Design studio's brand kit (colours, fonts, logo, voice) is already in your prompt when set. If it's empty, offer to read it from the store URL (Design › Brand › From website) before making visuals.

## 2. Plan the week
- Mix formats: product showcase, problem → solution, social proof (review/UGC), behind the scenes / how it's made or packed, tip or how-to, offer/CTA. Never 5 hard sells in a row (rule of thumb: 4 value : 1 sell).
- For each day: date, platform, format (post 1080×1350, story/reel 1080×1920), hook (first line ≤ 8 words), caption (≤ 120 words, one CTA), 5-10 hashtags (mix niche + broad, no banned or irrelevant tags), visual brief, best time to post (local).
- Show it as a compact table first. Change what the seller asks before building anything.

## 3. Make the visuals
- For each approved post: New design in the Design studio with the matching artboard (`post` 1080×1350 or `story` 1080×1920), following the brand kit exactly (palette, fonts, logo placement, safe zones: keep text out of the top and bottom 250 px on stories).
- One clear message per visual; product image large; price or offer in the accent colour; ≤ 12 words of text on the image.
- Export each to PNG into `content/<yyyy-mm-dd>/` with the caption in `caption.txt` next to it. Low-data mode: skip renders and give the visual brief only.

## 4. Schedule
- After the seller approves, add one Schedule item per post with `schedule` (action add, name `post-<date>-<platform>`, when `once YYYY-MM-DD HH:MM` at the posting time, prompt: "Post reminder: <platform> · <hook>. Files: content/<date>/. Caption ready in caption.txt.").
- Blaze does not post on the seller's accounts by itself; it reminds and has everything ready.

## Checklist
- [ ] Store, audience, platforms, goals and products known.
- [ ] Week mixes formats (4 value : 1 sell), each with hook, caption, hashtags, time.
- [ ] Table approved before designing.
- [ ] Visuals follow the brand kit and safe zones; exported with captions.
- [ ] One `once` Schedule item per post; nothing posted without the seller.
