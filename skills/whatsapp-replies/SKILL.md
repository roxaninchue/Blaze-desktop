---
name: whatsapp-replies
description: Draft WhatsApp replies to customers and leads on the desktop - order status, delivery questions, prices, complaints, follow-ups - in the store's voice, then send them through the paired Android phone over Blaze Link only after the seller approves each message. Use for "reply to this customer", "answer my WhatsApp messages", "follow up with buyers".
tags: [whatsapp, customer-service, messages, ecommerce, blaze-link, approval]
version: 1
---

# WhatsApp replies (draft here, send from the phone)

Blaze drafts on the PC; the paired phone sends. Every single send needs the seller's explicit yes.

## 0. Needs
- Blaze Link: a phone paired in the Phone view (`/phone`). Without it, still draft the replies and show them to copy.
- `phone_ask` asks the PC user for approval before any send (it can't be skipped, even in Autopilot) and Blaze Android asks again on the phone.

## 1. Get the conversation
- If the seller pastes messages or a screenshot, use those. With a paired phone, ask it to read them: `phone_ask` "Read my latest unread WhatsApp messages from customers (names and text only)". Reading doesn't send anything.
- For order questions, look the order up first (Shopify admin export, an order sheet, or the Shopify Admin API through api-integration if the seller set it up). Never guess an order status or tracking number.

## 2. Draft
- One reply per customer. Short (≤ 60 words), warm, specific: their name, their order, the next concrete step and when. No Markdown; WhatsApp *bold* only if helpful; at most one emoji.
- Patterns: order status (status + tracking link + expected date), delivery delay (apology + new date + small goodwill if the seller allows), price/availability (price, variants, how to order), complaint (acknowledge, ask for a photo, offer the store's real return/refund policy), follow-up after delivery (thanks + review request).
- Never promise refunds, discounts or dates the seller hasn't approved; put `[seller: confirm]` instead.

## 3. Approve, then send
- Show each draft with the recipient. Wait for "send", edits or "skip". Approval covers that exact text and recipient only.
- Send one message per `phone_ask` call: "Send WhatsApp to <name / number>: <exact text>". The PC shows "Send through your phone?" with the text; the phone may ask again. If either says no, nothing is sent.
- After sending, report who got what. Never batch-send unapproved messages, never message people who didn't write first, never send marketing blasts.

## Checklist
- [ ] Conversation and order facts gathered; nothing guessed.
- [ ] Drafts short, specific, in the store's voice; unapproved promises marked.
- [ ] Each draft shown with its recipient and approved individually.
- [ ] Sent one by one via phone_ask (approval on PC and phone).
- [ ] Report of what was sent; nothing sent without a yes.
