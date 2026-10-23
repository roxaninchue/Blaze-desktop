---
name: diagrams
description: Make clear diagrams - flowcharts, mind maps, org charts, sequence and architecture diagrams, timelines, ER and state diagrams, user journeys - as Mermaid or hand-placed SVG that previews in Design, with PNG, SVG or PDF export. Use for any "diagram", "flowchart", "mind map", "chart how X works" or "map out" request.
tags: [diagrams, flowchart, mermaid, svg, visual, design]
version: 1
---

# Diagrams

Finish line: a diagram a stranger understands in ten seconds, open in Design, exported as the
user needs (PNG for chat, SVG for docs, PDF for print).

## 1. Pick the kind and the engine

| They want | Kind | Engine |
| --- | --- | --- |
| steps, decisions, a process | flowchart (top-down; left-right when wide and shallow) | Mermaid `flowchart` |
| ideas around one topic | mind map | Mermaid `mindmap`, or SVG for a designed one |
| who reports to whom | org chart | Mermaid `flowchart TB` |
| messages between systems over time | sequence | Mermaid `sequenceDiagram` |
| services, data stores, flows | architecture | SVG by hand (boxes in lanes) or Mermaid `flowchart` with subgraphs |
| events on dates | timeline | Mermaid `timeline` or SVG |
| tables and keys | ER | Mermaid `erDiagram` |
| statuses and transitions | state | Mermaid `stateDiagram-v2` |
| a customer's steps and feelings | journey | Mermaid `journey` or SVG |

Mermaid when structure matters more than looks; hand-placed SVG when it will be presented or
branded. In a Markdown doc or a GitHub README, write a ```mermaid block straight into the file.

## 2. Make it readable

1. Write the content first: the nodes and edges as a list. Merge anything that says the same thing;
   more than ~15 nodes means split it into an overview plus detail diagrams.
2. Labels: 1-4 words on nodes, verbs on edges ("sends invoice", "if paid"). Decisions are questions.
3. One direction of flow. No crossing edges if a reorder avoids them. Group related nodes in subgraphs or lanes.
4. Colour carries meaning only (one accent for the main path or the thing that changed, grey for
   external systems); add a small legend when colour means something.

## 3. Preview and export

- Design previews HTML, so wrap Mermaid in a page: `design/<name>-diagram.html` with
  `<pre class="mermaid">…</pre>` and
  `<script type="module">import m from "https://cdn.jsdelivr.net/npm/mermaid@11/dist/mermaid.esm.min.mjs"; m.initialize({startOnLoad:true, theme:"neutral"});</script>`.
- Files: `npx -y @mermaid-js/mermaid-cli -i d.mmd -o d.svg` (or `-o d.png -w 2000`, `-o d.pdf`).
  No Node: keep the HTML page and export from it with a headless browser, or hand-draw the SVG.
- Hand-placed SVG: a 1200-wide viewBox, 8 px grid, rounded rects, 2 px strokes, arrowheads from one
  `<marker>`, each node a `<g id>` so Design can retype or recolour it.
- Render to PNG and look at it before handing it over.

## Mixing with other skills

- Hand-drawn whiteboard look: structure from here, paper and pens from `paper-sketch`.
- In a deck: export SVG and place it with `slide-deck`. In a printed one-pager: `print-design`.
- From real data (an org export, a schema dump): read it with `data-analysis` first, then generate.

## Checklist

- [ ] Right kind for the question; one flow direction; no avoidable crossings.
- [ ] Short labels; decisions phrased as questions; ~15 nodes or fewer per diagram.
- [ ] Rendered and checked; exported in the format the user needs.
