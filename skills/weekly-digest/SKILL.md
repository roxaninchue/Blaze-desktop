---
name: weekly-digest
description: Set up and run a weekly store digest - last week's orders and revenue, best sellers, top social posts and what to post next - as a scheduled Blaze job that sends a short report every week. Use for "weekly report for my store", "how did HomeGlow do this week", "send me a summary every Sunday".
tags: [ecommerce, analytics, report, schedule, social-media, shopify]
version: 1
---

# Weekly digest

Every week, one short report: what sold, what worked on social, what to do next.

## 1. Sources (set up once, remember them)
- **Orders**: Shopify Admin API (api-integration skill: a custom app with `read_orders` and `read_products`, token saved in Blaze's key store, never in the prompt or a file in the repo), or a weekly orders CSV export the seller drops in a folder, or an order sheet.
- **Social**: Instagram/Facebook insights export, TikTok analytics export, or numbers the seller pastes. Without access, ask for the top 3 posts' reach and clicks.
- Save the choices with `remember` (store name, source type, folder paths, currency, report day).

## 2. Build the digest (`reports/weekly-<yyyy-ww>.md`)
- **Headline**: revenue, orders, average order value vs the previous week (↑/↓ %).
- **Best sellers**: top 5 products by units and revenue; anything out of stock or low.
- **Problems**: refunds, cancellations, unfulfilled orders older than 3 days, repeated customer complaints.
- **Social**: top 3 posts by reach or clicks, with why they worked (hook, format, product).
- **Next week**: 3-5 suggested posts (tie them to best sellers and what worked; hand off to content-calendar), one ad to test (ad-lab), one store fix.
- Under 250 words. Numbers exact from the data, currency labelled; say "no data" rather than guessing.

## 3. Schedule it
- After the seller agrees: `schedule` action add, name `store-weekly-digest`, when `weekly sun 18:00` (or their day/time), prompt: "Run the weekly-digest skill for <store>: read <sources>, write reports/weekly-<week>.md, and notify me with the headline and next week's 3 posts."
- The job runs unattended: it can read data and write the report, but sends nothing to customers. A `notify` shows the headline on the PC; with Blaze Link the seller can ask the phone for it.

## Checklist
- [ ] Order and social sources chosen and remembered; tokens kept in the key store.
- [ ] Digest has headline vs last week, best sellers, problems, social, next steps.
- [ ] Under 250 words; every number from the data.
- [ ] Weekly Schedule job added after approval; it never messages customers.
