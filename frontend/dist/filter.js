// Blaze picker filter (v0.7.4): one ranker for every list you narrow by typing in the composer:
// the "/" command menu, its pickers (/skills, /model, /project, /theme, + › Commands) and @files.
// Name first (prefix > word-start > substring > fuzzy initials), then tags, then description
// (two letters or more).
// Works in the page (window.PickFilter) and in node (require) for the unit tests.
(function (root) {
  const SEP = /[\s\-_./:\\@·]/;
  const wordStart = (s, i) => i === 0 || SEP.test(s[i - 1]) ||
    (/[a-z0-9]/.test(s[i - 1]) && /[A-Z]/.test(s[i]));
  const span = (a, n) => Array.from({ length: n }, (_, k) => a + k);

  // Fuzzy: the first letter starts a word, every next letter either follows the previous
  // one or starts a later word. "wdb" → web-design-blaze, "sli" ↛ shell-sysadmin.
  function fuzzy(name, q) {
    const n = name.toLowerCase();
    const go = (qi, from, prev) => {
      if (qi === q.length) return [];
      for (let i = from; i < n.length; i++) {
        if (n[i] !== q[qi]) continue;
        if (!(i === prev + 1 && qi > 0) && !wordStart(name, i)) continue;
        const rest = go(qi + 1, i + 1, i);
        if (rest) return [i].concat(rest);
      }
      return null;
    };
    return go(0, 0, -2);
  }

  // How well one string matches: {tier, at, pos} or null. Lower tier is better.
  function matchName(name, q) {
    name = String(name || "");
    if (!q) return { tier: 0, at: 0, pos: [] };
    const n = name.toLowerCase();
    if (n.startsWith(q)) return { tier: 0, at: 0, pos: span(0, q.length) };
    let first = -1;
    for (let k = n.indexOf(q); k >= 0; k = n.indexOf(q, k + 1)) {
      if (first < 0) first = k;
      if (wordStart(name, k)) return { tier: 1, at: k, pos: span(k, q.length) };
    }
    if (first >= 0) return { tier: 2, at: first, pos: span(first, q.length) };
    const f = fuzzy(name, q.replace(/\s+/g, ""));
    if (f && f.length) return { tier: 3, at: f[0], pos: f };
    return null;
  }

  // Every word of the query somewhere in the text: the positions of each, or null.
  function matchWords(text, q) {
    const t = String(text || "").toLowerCase(), pos = [];
    for (const w of q.split(/\s+/).filter(Boolean)) {
      const k = t.indexOf(w); if (k < 0) return null;
      pos.push(...span(k, w.length));
    }
    return pos.length ? { at: pos[0], pos } : null;
  }

  // rank(items, query, {name, desc, tags}) → matching items, best first, each a copy with
  // _pos (matched letters in the name) and _dpos (matched letters in the description).
  function rank(items, query, get = {}) {
    const q = String(query || "").trim().toLowerCase();
    const nm = get.name || (it => it.name), ds = get.desc || (it => it.desc), tg = get.tags || (it => it.tags);
    const out = [];
    (items || []).forEach((it, i) => {
      const name = nm(it) || "";
      let m = matchName(name, q), dpos = [];
      if (!m && q.includes(" ")) m = matchName(name, q.replace(/\s+/g, "-"));
      if (!m) {
        const tags = (tg(it) || []).map(t => String(t).toLowerCase());
        const ti = tags.findIndex(t => t.startsWith(q)), tj = ti < 0 ? tags.findIndex(t => t.includes(q)) : -1;
        if (ti >= 0 || tj >= 0) m = { tier: 4, at: ti >= 0 ? ti : 10 + tj, pos: [] };
      }
      if (!m && q.length > 1) { // one letter in a description is noise
        const d = matchWords(ds(it), q);
        if (d) { m = { tier: 5, at: d.at, pos: [] }; dpos = d.pos; }
      }
      if (m) out.push({ it, i, m, dpos });
    });
    out.sort((a, b) => a.m.tier - b.m.tier || a.m.at - b.m.at || a.i - b.i);
    return out.map(r => ({ ...r.it, _pos: r.m.pos, _dpos: r.dpos }));
  }

  const ESC = { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" };
  const esc1 = c => ESC[c] || c;
  // The text, escaped, with the matched letters wrapped in <mark class="hit"> (runs merged).
  function hl(text, pos) {
    text = String(text == null ? "" : text);
    if (!pos || !pos.length) return text.replace(/[&<>"']/g, esc1);
    const on = new Set(pos);
    let html = "", open = false;
    for (let i = 0; i < text.length; i++) {
      const hit = on.has(i);
      if (hit && !open) { html += '<mark class="hit">'; open = true; }
      if (!hit && open) { html += "</mark>"; open = false; }
      html += esc1(text[i]);
    }
    return html + (open ? "</mark>" : "");
  }

  const api = { rank, hl, matchName, fuzzy };
  if (typeof module !== "undefined" && module.exports) module.exports = api;
  root.PickFilter = api;
})(typeof window !== "undefined" ? window : globalThis);
