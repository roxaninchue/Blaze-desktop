// v0.4.2 polish: AI chat titles (animated, rename on double-click) and the emblem marks (v0.18.1 ThinkingOrb).
// Loaded after app.js; it only uses app.js globals (API, on, $, $$, h, esc, current) and small hooks.
(() => {
  // ---------------- chat titles ----------------
  const latest = {};   // id -> newest title we were told about (beats a stale list fetch)
  let editing = null;  // id of the chat being renamed
  const downs = {};    // id -> last mousedown time (double-click survives a list rebuild)

  // Typewriter: the old title fades out, the new one types in (v0.18.1: no caret).
  function typeTitle(el, text) {
    if (!el || el.textContent === text) return;
    if (window.BlazeMotion && !window.BlazeMotion.enabled) { el.textContent = text; return; }
    clearInterval(el._tw);
    el.classList.add("tfade");
    setTimeout(() => {
      el.classList.remove("tfade");
      el.classList.add("typing");
      el.textContent = "";
      let i = 0;
      const chars = [...text];
      el._tw = setInterval(() => {
        el.textContent = chars.slice(0, ++i).join("");
        if (i >= chars.length) { clearInterval(el._tw); setTimeout(() => el.classList.remove("typing"), 700); }
      }, Math.max(18, Math.min(42, 900 / chars.length)));
    }, 220);
  }

  function startRename(b, c) {
    const t = $(".t", b);
    if (!t || $("input", b)) return;
    editing = c.id;
    const inp = h("input", "rename");
    inp.value = latest[c.id] || c.title || "";
    inp.setAttribute("aria-label", "Rename chat");
    inp.maxLength = 80;
    t.replaceWith(inp);
    b.classList.add("editing");
    inp.focus(); inp.select();
    let done = false;
    const finish = async save => {
      if (done) return; done = true; editing = null;
      const v = inp.value.trim();
      const span = h("span", "t", esc(save ? (v || c.title) : (latest[c.id] || c.title)));
      inp.replaceWith(span); b.classList.remove("editing");
      if (!save || v === (latest[c.id] || c.title)) return;
      const final = await API().RenameConversation(c.id, v);
      latest[c.id] = final || v; c.source = v ? "manual" : "";
      b.dataset.src = c.source;
      span.textContent = latest[c.id];
      if (!v) span.title = "Back to automatic titles";
    };
    inp.onkeydown = e => { e.stopPropagation(); if (e.key === "Enter") finish(true); if (e.key === "Escape") finish(false); };
    inp.onblur = () => finish(true);
    inp.onclick = e => e.stopPropagation();
  }

  // Called by refreshSide for every chat row.
  function decorate(b, c) {
    b.dataset.id = c.id;
    b.dataset.src = c.source || "";
    const t = $(".t", b);
    if (latest[c.id] && latest[c.id] !== c.title && t) { t.textContent = latest[c.id]; c.title = latest[c.id]; }
    if (t) t.title = c.title + (c.source === "manual" ? " · renamed" : c.source === "auto" ? " · named by Blaze · double-click to rename" : " · double-click to rename");
    b.addEventListener("mousedown", e => {
      if (e.button !== 0 || e.target.closest(".x, input")) return;
      const now = performance.now(), prev = downs[c.id] || 0;
      downs[c.id] = now;
      if (now - prev < 420) { e.preventDefault(); downs[c.id] = 0; startRename(b, c); }
    });
    b.addEventListener("dblclick", e => { if (!e.target.closest(".x, input")) startRename(b, c); });
    if (editing === c.id) setTimeout(() => startRename(b, c), 0);
  }

  on("title", d => {
    if (!d || !d.id || !d.title) return;
    latest[d.id] = d.title;
    if (editing === d.id) return;
    const b = $(`#chats .chat[data-id="${CSS.escape(d.id)}"]`);
    if (b) { typeTitle($(".t", b), d.title); b.dataset.src = d.source || "auto"; }
  });

  // ---------------- ThinkingOrb marks (v0.18.1) ----------------
  // Every work mark (the Blaze label, the "Working / Worked" card, each step, answer cards, specialist rows) is a
  // small emblem: the glass drop in its gold halo (duo.js). It moves only while that work is live and the emblem is
  // on screen; when the work ends, or under reduced motion, it keeps one still frame.
  const SEL = ".msg.blaze .who .mk, .msg.blaze .work .sq, .step .st, .acard .amk, .sprow .sp-mk";
  const SIZE = host => host.matches(".acard .amk") ? 16 : host.matches(".who .mk") ? 20 : 18;
  const marks = new Set();

  const isLive = m => !!(m.closest(".work.live, .step.run, .thinking, .acard.live, .sprow.live") ||
    (typeof current !== "undefined" && current && current.el && m.closest(".who") && m.closest(".msg.blaze") === current.el));

  function attach(host) {
    if (host._orb) return;
    const m = document.createElement("blaze-emblem");
    m.className = "morb";
    m.setAttribute("size", SIZE(host));
    m.setAttribute("aria-hidden", "true");
    host._orb = m; host.classList.add("has-orb");
    host.append(m);
    marks.add(m);
    m.live = isLive(m);
  }

  // Start or stop each mark's clock to match its work.
  function kick() {
    marks.forEach(m => {
      if (!m.isConnected) { marks.delete(m); return; }
      const l = isLive(m);
      if (l !== m.live) m.live = l;
    });
  }

  function scan(root) {
    if (root.nodeType !== 1) return;
    if (root.matches(SEL)) attach(root);
    root.querySelectorAll(SEL).forEach(attach);
  }
  new MutationObserver(ms => {
    ms.forEach(r => r.addedNodes.forEach(n => { if (n.nodeType === 1) scan(n); }));
    kick();   // class changes (work.live → done, step.run → ok) start or stop the clocks
  }).observe(document.body, { childList: true, subtree: true, attributes: true, attributeFilter: ["class"] });
  scan(document.body);
  ["delta", "tool", "tool_done", "status", "thinking", "done", "error", "stopped"].forEach(ev => on(ev, () => setTimeout(kick, 0)));

  // ---------------- personality (Settings › Behaviour) ----------------
  function personalityGroup(redraw) {
    const g = group("Personality", "How Blaze sounds in chat. Scheduled jobs always run plain.");
    const mode = (S.prefs.personality || "default");
    const seg = h("div", "seg");
    const box = h("div", "pbox");
    [["default", "Default"], ["plain", "Plain"], ["custom", "Custom"]].forEach(([k, t]) => {
      const b = h("button", mode === k ? "on" : "", t);
      b.onclick = async () => {
        let custom = S.prefs.personality_custom || "";
        if (k === "custom" && !custom.trim()) custom = (await API().DefaultPersonality() || "").replace(/^PERSONALITY\n/, "");
        await API().SetPersonality(k, custom);
        S.prefs.personality = k; S.prefs.personality_custom = custom; redraw();
      };
      seg.append(b);
    });
    g.append(seg, box);
    if (mode === "default") {
      box.append(h("div", "td pnote", "Warm, sharp and one step ahead: answers first, takes a side, specific, honest about being an assistant."));
    } else if (mode === "plain") {
      box.append(h("div", "td pnote", "No personality block: just Blaze's rules and your style note below."));
    } else {
      const ta = h("textarea", "field pcustom"); ta.value = S.prefs.personality_custom || ""; ta.rows = 9;
      ta.placeholder = "Describe how Blaze should talk and behave.";
      const sv = h("button", "btn line sm", "Save personality"); sv.style.marginTop = "8px";
      sv.onclick = async () => { await API().SetPersonality("custom", ta.value); S.prefs.personality_custom = ta.value.trim(); sv.textContent = "Saved"; setTimeout(() => sv.textContent = "Save personality", 1200); };
      box.append(ta, sv);
    }
    return g;
  }

  window.Polish = { decorate, typeTitle, startRename, kick, personalityGroup };
})();
