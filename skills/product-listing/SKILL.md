---
name: product-listing
description: Turn a supplier product URL (AliExpress, Zendrop, CJ Dropshipping, Alibaba, Temu) into a ready Shopify listing - title, benefit-led description, bullet specs, tags, SEO title and meta, price suggestion and image prompts. Use for "write a listing for this product", "add this AliExpress item to my store", "product description".
tags: [ecommerce, shopify, dropshipping, copywriting, seo, product]
version: 1
---

# Product listing

From a supplier page to a listing that sells honestly.

## 1. Read the supplier page
- `fetch_page` the URL. Pull: product name, every variant (colour, size, plug type), materials, dimensions and weight, power/battery specs, what's in the box, shipping times to the seller's market, supplier price per variant, and image URLs.
- If the page is blocked or thin, try the mobile URL or ask the seller to paste the details. Never invent specs, certifications or reviews; leave `[check: …]` where something is missing.
- Note claims you must not copy (fake "FDA approved", "100% waterproof" without a rating, trademarked brand names).

## 2. Write the listing
- **Title** (≤ 70 chars): what it is + top benefit + key variant. No supplier codes, no ALL CAPS, no competitor brands.
- **Description**: 1-line hook on the problem it solves → 2-3 short benefit paragraphs (how it feels at home, who it's for) → bullet specs (exact numbers from the supplier) → what's in the box → shipping & returns line from the store's policy.
- Voice: the store's brand voice (brand kit / memory); for HomeGlow, warm and cosy, plain words.
- **Tags** (8-15): product type, room, style, use, gift occasions. **Collections** suggestion.
- **SEO**: page title ≤ 60 chars, meta description ≤ 155 chars, URL handle.
- **Price**: suggest a price from supplier cost + shipping (aim 2.5-3× landed cost, round to .99 or the market's habit) and a compare-at only if it's honest.

## 3. Images
- 4-6 image prompts for the Design studio or an image tool: hero on white, lifestyle in a real room, scale shot (in hand / next to a sofa), detail close-up, variant grid, and a benefit infographic with ≤ 6 words. Use the brand kit colours for backgrounds and text.
- Remind the seller to check image rights; supplier photos may only be used if the supplier allows it.

## 4. Deliver
- Output as Markdown and as a CSV row matching Shopify's product import columns (Handle, Title, Body (HTML), Vendor, Type, Tags, Variant Price, Variant SKU…). Save to `listings/<handle>.md` and `.csv`.
- Offer next steps: ad-lab variants, a content-calendar post, or adding it through the Shopify Admin API (api-integration skill) after approval.

## Checklist
- [ ] Every spec comes from the supplier page; gaps marked [check].
- [ ] Title ≤ 70, SEO title ≤ 60, meta ≤ 155 characters.
- [ ] Description leads with benefits, then exact specs and box contents.
- [ ] Tags, collection, price with the maths shown.
- [ ] 4-6 image prompts; image rights reminder.
- [ ] Saved as .md and Shopify-import .csv.
