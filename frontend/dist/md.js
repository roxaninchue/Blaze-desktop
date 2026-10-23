// Small, safe Markdown renderer in Blaze's style (escapes all HTML first).
(function () {
  const esc = s => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");

  function inline(s) {
    s = esc(s);
    const codes = [];
    s = s.replace(/`([^`]+)`/g, (_, c) => { codes.push(c); return "\u0000" + (codes.length - 1) + "\u0000"; });
    s = s.replace(/!\[([^\]]*)\]\((https?:\/\/[^\s)]+)\)/g, (_, t, u) => `<a data-href="${u}">${t || "picture"}</a>`);
    s = s.replace(/\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)/g, (_, t, u) => `<a data-href="${u}">${t}</a>`);
    s = s.replace(/(^|[\s(])(https?:\/\/[^\s<)]+)/g, (_, p, u) => `${p}<a data-href="${u}">${u}</a>`);
    s = s.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
    s = s.replace(/(^|[^*\w])\*([^*\n]+)\*(?!\w)/g, "$1<em>$2</em>");
    s = s.replace(/\u0000(\d+)\u0000/g, (_, i) => `<code>${codes[+i]}</code>`);
    return s;
  }

  // ---------- v0.17.1 rich replies ----------
  // W2: /csvkit/chart/<id>.png is a chart csv_report drew on this computer (asset server).
  const IMG = /!\[([^\]]*)\]\((https:\/\/[^\s)]+|\/csvkit\/chart\/[0-9a-f]{16}\.(?:png|svg))\)/g;
  const IMGLINE = /^\s*(!\[[^\]]*\]\((https:\/\/[^\s)]+|\/csvkit\/chart\/[0-9a-f]{16}\.(?:png|svg))\)\s*)+$/;
  const FLOW = /^\*\*([^*]+)\*\*\s*[:.\u2014\u2013-]?\s*(.*)$/;
  // Low-data mode: no pictures or diagrams (they stay as text); MD.opts can switch parts off.
  const opts = { images: true, diagrams: true, flow: true };
  const lean = () => { try { return typeof S !== "undefined" && S && S.prefs && !!S.prefs.low_data; } catch (e) { return false; } };
  const rich = k => opts[k] !== false && (k === "flow" || !lean());
  const b64 = t => { try { return btoa(unescape(encodeURIComponent(t))).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, ""); } catch (e) { return ""; } };

  // Follow-ups (mobile ReplyParts): "<<follow-ups: a | b | c>>" at the end of a reply becomes
  // pills; a half-typed marker is hidden while it streams. Tolerant of one or two brackets.
  const FU = /<{1,2}[\s\/(\[]*follow[\s_-]?ups?\s*:([\s\S]*?)([)\]]?\s*>{1,3}|$)/i;
  function splitFollowups(src, final) {
    let text = String(src || ""), list = [];
    const m = FU.exec(text);
    if (m && (m[2] || (final && m[1].includes("|")))) {
      list = m[1].split("|").map(x => x.trim().replace(/^["'\-•\s<>/]+|["'\-•\s<>/]+$/g, "")).filter(Boolean).slice(0, 3);
      text = text.slice(0, m.index);
    } else if (m) text = text.slice(0, m.index);
    const tail = /<{1,2}\s*\/?\s*(f(o(l(l(o(w[\s_-]?(u(ps?)?)?)?)?)?)?)?)?\s*$/i.exec(text);
    if (tail && tail[0].length < 20) text = text.slice(0, tail.index);
    return { text: text.replace(/\s+$/, ""), followups: list };
  }

  function render(src) {
    const lines = src.replace(/\r/g, "").split("\n");
    let out = [], i = 0;
    while (i < lines.length) {
      let l = lines[i];
      const fence = l.match(/^\s*```\s*([\w+#.-]*)/);
      if (fence) {
        const buf = []; i++;
        while (i < lines.length && !/^\s*```/.test(lines[i])) buf.push(lines[i++]);
        i++;
        const lang = (fence[1] || "").toLowerCase();
        if (lang === "mermaid" && rich("diagrams") && buf.join("").trim()) {
          // v0.17.1 inline diagram (mobile v0.24.9): drawn by mermaid.ink, the source one click away.
          out.push(`<div class="code diagram"><div class="bar"><span class="label">diagram</span><button class="copy">Copy</button><button class="dsrc">Source</button></div><div class="dimg"><img loading="lazy" referrerpolicy="no-referrer" alt="Diagram" src="https://mermaid.ink/svg/${b64(buf.join("\n"))}"></div><pre>${esc(buf.join("\n"))}</pre></div>`);
          continue;
        }
        out.push(`<div class="code"><div class="bar"><span class="label">${esc(fence[1] || "code")}</span><button class="copy">Copy</button></div><pre>${esc(buf.join("\n"))}</pre></div>`);
        continue;
      }
      if (/^\s*$/.test(l)) { i++; continue; }
      if (IMGLINE.test(l) && rich("images")) {
        // v0.17.1 pictures (mobile v0.24.9): one beside the paragraph after it, 2-3 side by side, 4+ as a deck.
        const pics = [];
        while (i < lines.length && IMGLINE.test(lines[i])) { for (const m of lines[i].matchAll(IMG)) pics.push({ alt: m[1], url: m[2] }); i++; }
        const tag = p => `<img loading="lazy" referrerpolicy="no-referrer" alt="${esc(p.alt)}" src="${esc(p.url)}">`;
        const next = lines[i];
        if (pics.length === 1 && next && next.trim() && !/^\s*(```|#{1,6}\s|>|[-*•]\s|\d+[.)]\s|\||!\[)/.test(next)) {
          const buf = [];
          while (i < lines.length && lines[i].trim() && !/^\s*(```|#{1,6}\s|>|[-*•]\s|\d+[.)]\s|\||!\[)/.test(lines[i])) buf.push(lines[i++]);
          out.push(`<div class="rside">${tag(pics[0])}<p>${buf.map(inline).join("<br>")}</p></div>`);
        } else if (pics.length === 1) out.push(`<figure class="rimg">${tag(pics[0])}</figure>`);
        else if (pics.length <= 3) out.push(`<div class="rgroup" style="--n:${pics.length}">${pics.map(tag).join("")}</div>`);
        else out.push(`<div class="deck" data-i="0">${pics.map((p, k) => `<div class="dcard" data-o="${Math.min(k, 3)}">${tag(p)}</div>`).join("")}<button class="dnav prev" aria-label="Previous picture">‹</button><button class="dnav next" aria-label="Next picture">›</button><span class="dcount">1 / ${pics.length}</span></div>`);
        continue;
      }
      let h = l.match(/^(#{1,6})\s+(.*)/);
      if (h) { const n = Math.min(h[1].length, 3); out.push(`<h${n}>${inline(h[2])}</h${n}>`); i++; continue; }
      if (/^\s*(---|\*\*\*)\s*$/.test(l)) { out.push("<hr>"); i++; continue; }
      if (/^\s*>/.test(l)) {
        const buf = [];
        while (i < lines.length && /^\s*>/.test(lines[i])) buf.push(lines[i++].replace(/^\s*>\s?/, ""));
        out.push(`<blockquote>${inline(buf.join(" "))}</blockquote>`); continue;
      }
      if (/^\s*\|.*\|\s*$/.test(l) && i + 1 < lines.length && /^\s*\|[\s:|-]+\|\s*$/.test(lines[i + 1])) {
        const cells = r => r.trim().replace(/^\||\|$/g, "").split("|").map(c => inline(c.trim()));
        let t = "<table><tr>" + cells(l).map(c => `<th>${c}</th>`).join("") + "</tr>";
        i += 2;
        while (i < lines.length && /^\s*\|.*\|\s*$/.test(lines[i])) t += "<tr>" + cells(lines[i++]).map(c => `<td>${c}</td>`).join("") + "</tr>";
        out.push(`<div class="tbl">${t}</table></div>`); continue;
      }
      if (/^\s*([-*•])\s+/.test(l) || /^\s*\d+[.)]\s+/.test(l)) {
        const ordered = /^\s*\d+[.)]\s+/.test(l), tag = ordered ? "ol" : "ul", items = [];
        while (i < lines.length && (ordered ? /^\s*\d+[.)]\s+/ : /^\s*([-*•])\s+/).test(lines[i])) {
          items.push(lines[i].replace(/^\s*([-*•]|\d+[.)])\s+/, "")); i++;
          while (i < lines.length && /^\s{2,}\S/.test(lines[i]) && !/^\s*([-*•]|\d+[.)])\s+/.test(lines[i])) items[items.length - 1] += " " + lines[i++].trim();
        }
        // v0.17.1 step-flow (mobile v0.24.9): a numbered list whose items all start with a **bold title**.
        if (ordered && items.length >= 2 && rich("flow") && items.every(x => FLOW.test(x))) {
          out.push(`<ol class="flow">${items.map((x, k) => { const m = x.match(FLOW); return `<li><span class="fdot">${k + 1}</span><span class="ftitle">${inline(m[1])}</span>${m[2].trim() ? `<span class="fdesc">${inline(m[2].trim())}</span>` : ""}</li>`; }).join("")}</ol>`);
          continue;
        }
        out.push(`<${tag}>${items.map(x => `<li>${inline(x)}</li>`).join("")}</${tag}>`); continue;
      }
      const buf = [];
      while (i < lines.length && lines[i].trim() && !/^\s*(```|#{1,6}\s|>|[-*•]\s|\d+[.)]\s|\|)/.test(lines[i])) buf.push(lines[i++]);
      if (!buf.length) { buf.push(lines[i++]); }
      out.push(`<p>${buf.map(inline).join("<br>")}</p>`);
    }
    return out.join("");
  }

  // ---------- v0.4.1 answer cards ----------
  // The model puts long material in ":::card Title" ... ":::" blocks. answer() pulls them
  // out of the reply; the app shows each as a card above the short answer.
  const OPEN = /^\s*:::\s*card\b[:\s]*(.*)$/i, CLOSE = /^\s*:::\s*$/, FENCE = /^\s*```/;
  const cleanTitle = t => String(t || "").replace(/^#+\s*/, "").replace(/\*\*|__|`/g, "").replace(/^["']|["']$/g, "").trim().slice(0, 90);

  function parseCards(src, final) {
    const lines = src.replace(/\r/g, "").split("\n");
    const cards = [], rest = [];
    let cur = null, fence = false;
    for (const l of lines) {
      if (!cur) {
        const m = !fence && l.match(OPEN);
        if (m) { cur = { title: cleanTitle(m[1]) || "Details", lines: [], closed: false }; cards.push(cur); continue; }
        if (FENCE.test(l)) fence = !fence;
        if (!fence && CLOSE.test(l)) continue; // a stray closer
        rest.push(l);
      } else {
        if (FENCE.test(l)) fence = !fence;
        if (!fence && CLOSE.test(l)) { cur.closed = true; cur = null; continue; }
        cur.lines.push(l);
      }
    }
    if (!final) {
      // Hide a marker that is still arriving (":", "::", ":::ca").
      const partial = /^\s*:{1,3}\s*(c(a(r(d)?)?)?)?\s*$/i;
      const tail = cur ? cur.lines : rest;
      if (tail.length && partial.test(tail[tail.length - 1])) tail.pop();
    }
    return {
      cards: cards.map(c => ({ title: c.title, body: c.lines.join("\n").replace(/^\s*\n|\s+$/g, ""), live: !c.closed && !final })).filter(c => c.body || c.live),
      rest: rest.join("\n").trim(),
    };
  }

  // Top-level blocks of a reply (paragraph, list, quote, heading, table, code).
  function blocks(src) {
    const lines = src.replace(/\r/g, "").split("\n"), out = [];
    const isTable = i => /^\s*\|.*\|\s*$/.test(lines[i]) && i + 1 < lines.length && /^\s*\|[\s:|-]+\|\s*$/.test(lines[i + 1]);
    let i = 0;
    while (i < lines.length) {
      const l = lines[i];
      if (/^\s*$/.test(l)) { i++; continue; }
      const s = i;
      if (FENCE.test(l)) {
        i++; while (i < lines.length && !FENCE.test(lines[i])) i++; i++;
        out.push({ type: "code", text: lines.slice(s, i).join("\n"), lang: (l.match(/```\s*([\w+#.-]*)/) || [])[1] || "" }); continue;
      }
      if (/^#{1,6}\s/.test(l)) { out.push({ type: "heading", text: l, title: cleanTitle(l) }); i++; continue; }
      if (isTable(i)) {
        i += 2; while (i < lines.length && /^\s*\|.*\|\s*$/.test(lines[i])) i++;
        out.push({ type: "table", text: lines.slice(s, i).join("\n") }); continue;
      }
      const type = /^\s*([-*•]|\d+[.)])\s+/.test(l) ? "list" : /^\s*>/.test(l) ? "quote" : "p";
      i++;
      while (i < lines.length && lines[i].trim() && !FENCE.test(lines[i]) && !/^#{1,6}\s/.test(lines[i]) && !isTable(i)) i++;
      out.push({ type, text: lines.slice(s, i).join("\n") });
    }
    return out;
  }

  // Fallback when the model ignored the card format: a long reply (or one with a table)
  // keeps its opening as the short answer and the rest goes into one card.
  function autoFold(src) {
    const text = src.trim();
    const bl = blocks(text);
    const nlines = text.split("\n").filter(x => x.trim()).length;
    if (!(text.length > 900 || nlines > 14 || bl.some(b => b.type === "table"))) return null;
    const big = b => b.type === "table" || b.type === "heading" || b.type === "code";
    let k = 0, len = 0;
    while (k < bl.length && !big(bl[k])) { len += bl[k].text.length; k++; }
    let lead;
    if (k > 0 && k < bl.length && len <= 600) lead = bl.slice(0, k);  // text before the first table/heading
    else lead = bl[0] && bl[0].type === "p" ? [bl[0]] : [];           // just the first paragraph
    let moved = bl.slice(lead.length);
    // A closing question ("Want me to commit it?") stays with the short answer.
    let tail = [];
    const last = moved[moved.length - 1];
    if (moved.length > 1 && last.type === "p" && last.text.length < 240 && /\?\s*$/.test(last.text)) { tail = [last]; moved = moved.slice(0, -1); }
    if (!moved.length) return null;
    // v0.19.15: a picture on its own (the chart of a CSV report) stays with the short answer, shown
    // whole, instead of being folded into the card below a table where only its top shows.
    const solo = b => b.type === "p" && IMGLINE.test(b.text.split("\n")[0]) && (b.text.split("\n")[0].match(IMG) || []).length === 1;
    const pics = moved.filter(solo);
    if (pics.length) { moved = moved.filter(b => !solo(b)); tail = pics.concat(tail); }
    if (!moved.length) return null;
    let title = "";
    if (moved[0].type === "heading") { title = moved[0].title; moved = moved.slice(1); }
    if (!title) { const hd = moved.find(b => b.type === "heading"); if (hd) title = hd.title; }
    if (!title && moved.length === 1 && moved[0].type === "code") title = moved[0].lang ? "Code · " + moved[0].lang : "Code";
    if (!moved.length) return null;
    return {
      cards: [{ title: title || "Details", body: moved.map(b => b.text).join("\n\n"), live: false, auto: true }],
      rest: lead.concat(tail).map(b => b.text).join("\n\n"),
    };
  }

  // answer(src, {fold, final}) -> {cards: [{title, body, live}], rest}
  function answer(src, o = {}) {
    src = String(src || "");
    if (!o.fold) {
      // Cards off: show the blocks inline, the title as a bold line.
      const lines = src.replace(/\r/g, "").split("\n"), out = [];
      let fence = false;
      for (const l of lines) {
        if (FENCE.test(l)) fence = !fence;
        const m = !fence && l.match(OPEN);
        if (m) { out.push(`**${cleanTitle(m[1]) || "Details"}**`, ""); continue; }
        if (!fence && CLOSE.test(l)) continue;
        out.push(l);
      }
      return { cards: [], rest: out.join("\n") };
    }
    const p = parseCards(src, o.final);
    if (!p.cards.length && o.final) return autoFold(src) || p;
    return p;
  }

  window.MD = { render, esc, answer, blocks, splitFollowups, opts };
})();
