---
name: print-design
description: Design things that get printed - posters, flyers, business cards, invitations, certificates, menus, stickers, labels, badges, T-shirt and merch graphics - at real print sizes with bleed, safe margins and print-ready PDF or PNG export. Use for any poster, flyer, card, invitation, sticker, label or "print this" request.
tags: [print, poster, flyer, business-card, pdf, design]
version: 1
---

# Print design

Finish line: a file the print shop accepts first time: right trim size, 3 mm bleed, text inside
the safe area, 300 dpi images, fonts embedded, as a PDF (or PNG for stickers and merch).

## 1. Sizes to know (trim size; add bleed on every side)

| Piece | Size |
| --- | --- |
| Poster | A3 297 × 420 mm, A2 420 × 594, US 18 × 24 in |
| Flyer | A5 148 × 210 mm, A4 210 × 297, US Letter 8.5 × 11 in |
| Business card | 85 × 55 mm (EU), 3.5 × 2 in (US) |
| Invitation / postcard | A6 105 × 148 mm, 5 × 7 in |
| Certificate | A4 or Letter, landscape |
| Sticker | the die-cut shape + 2 mm bleed; PNG with transparency at 300 dpi |
| T-shirt print | about 12 × 16 in at 300 dpi, transparent PNG |

Bleed 3 mm (0.125 in). Safe margin 5 mm inside the trim for all text and logos.

## 2. Build it as an HTML page

1. `design/<piece>.html` with `@page { size: <trim + 2×bleed>; margin: 0 }` and one `.sheet` element at
   that size in mm. Backgrounds and images run to the bleed edge; content sits inside the safe area.
2. Optional guides for the preview: thin dashed trim and safe-area lines in a `.guides` layer hidden by
   `@media print`.
3. Hierarchy for a poster or flyer: one dominant headline readable from 3 m, then when/where, then
   details, then the call to action (QR code, link). Business cards: name, role, one or two contacts.
4. Images 300 dpi at printed size (a full A4 image needs about 2480 × 3508 px). Vector logos as SVG.
5. Each block is its own element with an id so Design can retype, recolour and move it.

## 3. Export

- PDF: print the page with a headless browser at the sheet size, backgrounds on
  (Playwright `page.pdf({width, height, printBackground: true})`), or `wkhtmltopdf`/`weasyprint` when installed.
- Stickers and merch: PNG at 300 dpi with a transparent background; `magick` to check the dpi.
- Say what the printer may ask: CMYK conversion (the shop or `ghostscript -sColorConversionStrategy=CMYK`),
  crop marks if they want them, the paper weight that suits the piece.

## Mixing with other skills

- Hand-drawn invitation: art from `paper-sketch`, layout and export from here.
- Event poster with a floor plan or map: `floor-plan` or `diagrams` for the inset.
- Brand colours and type: the project's brand kit or `web-design-*` tokens.

## Checklist

- [ ] Trim size right, 3 mm bleed, text inside the 5 mm safe area.
- [ ] Images 300 dpi at size, logos vector, fonts embedded.
- [ ] Print-ready PDF (or 300 dpi PNG) exported and checked; printer notes given.
