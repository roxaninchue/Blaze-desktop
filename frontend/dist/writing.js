// Writing tools (v0.19.10): select text in the message box or in any reply, then Rewrite it in a
// tone or Translate it. The result shows next to the original, and nothing changes until you
// Accept. In the message box, Accept replaces exactly the text you selected (Undo puts it back);
// from a reply, Accept copies the result so you can paste it back into your document. Esc or
// Reject throws it away. One request at a time; a late answer for a rejected request is ignored.
(() => {
  let MENU = { tones: ["Clearer", "Shorter", "Friendlier", "More formal", "More casual", "Fix spelling and grammar"], languages: ["English", "French", "Spanish", "Portuguese", "Swahili"] };
  let sel = null;   // { text, source: "input" | "reply", start, end }
  let req = 0, bar = null, card = null;
  const input = () => document.getElementById("input");

  function loadMenu() { try { if (API().WritingMenu) API().WritingMenu().then(m => { if (m && m.tones) MENU = m; }); } catch (e) {} }

  function current() {
    const ta = input();
    if (ta && document.activeElement === ta && ta.selectionEnd - ta.selectionStart > 1) {
      return { text: ta.value.slice(ta.selectionStart, ta.selectionEnd), source: "input", start: ta.selectionStart, end: ta.selectionEnd };
    }
    const s = window.getSelection && window.getSelection();
    if (!s || s.isCollapsed || !s.rangeCount) return null;
    const node = s.anchorNode && (s.anchorNode.nodeType === 1 ? s.anchorNode : s.anchorNode.parentElement);
    if (!node || !node.closest || !node.closest(".msg.blaze .md, .msg .bubble")) return null;
    const text = String(s.toString());
    return text.trim().length > 1 ? { text, source: "reply" } : null;
  }

  function hideBar() { if (bar) { bar.remove(); bar = null; } }

  function showBar() {
    const s = current();
    if (!s) { hideBar(); return; }
    sel = s;
    if (!bar) {
      bar = h("div", "wbar", `<button class="wb-rw" title="Rewrite the selected text">Rewrite</button><button class="wb-tr" title="Translate the selected text">Translate</button>`);
      bar.addEventListener("mousedown", e => e.preventDefault()); // keep the selection
      bar.querySelector(".wb-rw").onclick = () => menu("rewrite", MENU.tones);
      bar.querySelector(".wb-tr").onclick = () => menu("translate", MENU.languages);
      document.body.append(bar);
    }
    let r;
    if (s.source === "input") r = input().getBoundingClientRect();
    else {
      const s2 = window.getSelection(), rg = s2.rangeCount ? s2.getRangeAt(0) : null;
      const el = rg && (rg.startContainer.nodeType === 1 ? rg.startContainer : rg.startContainer.parentElement);
      r = rg && rg.getBoundingClientRect ? rg.getBoundingClientRect() : el && el.getBoundingClientRect ? el.getBoundingClientRect() : null;
    }
    if (!r) { hideBar(); return; }
    bar.style.position = "fixed";
    bar.style.left = Math.max(8, Math.min(innerWidth - 220, (r.left || 0) + 8)) + "px";
    bar.style.top = Math.max(8, (r.top || 0) - 40) + "px";
  }

  function menu(mode, items) {
    const m = h("div", "wmenu");
    for (const it of items) { const b = h("button", "", esc(it)); b.onclick = () => { m.remove(); run(mode, it); }; m.append(b); }
    const other = h("form", "wother", `<input class="field" placeholder="${mode === "translate" ? "Another language" : "Describe a tone"}" maxlength="60"><button class="btn xs">Go</button>`);
    other.onsubmit = e => { e.preventDefault(); const v = other.querySelector("input").value.trim(); if (v) { m.remove(); run(mode, v); } };
    m.append(other);
    m.addEventListener("mousedown", e => { if (e.target.tagName !== "INPUT") e.preventDefault(); });
    (bar || document.body).append(m);
    setTimeout(() => addEventListener("mousedown", function off(ev) { if (!m.contains(ev.target)) { m.remove(); removeEventListener("mousedown", off, true); } }, true), 0);
  }

  function closeCard() { if (card) { card.remove(); card = null; } req++; }

  async function run(mode, arg) {
    if (!sel) return;
    const s = sel, id = ++req;
    hideBar(); if (card) card.remove();
    card = h("div", "wcard", `<div class="wc-h"><b>${mode === "translate" ? "Translate to " + esc(arg) : "Rewrite: " + esc(arg)}</b><button class="btn ghost xs wc-x" title="Reject (Esc)">✕</button></div>
      <div class="wc-cols"><div><div class="label">Original</div><div class="wc-orig"></div></div><div><div class="label">${mode === "translate" ? "Translation" : "New version"}</div><div class="wc-new wc-wait">Working…</div></div></div>
      <div class="wc-note small sub" hidden></div>
      <div class="wc-acts"><button class="btn primary sm wc-ok" disabled>${s.source === "input" ? "Accept" : "Accept and copy"}</button><button class="btn line sm wc-copy" disabled>Copy</button><button class="btn ghost sm wc-again" disabled>Try again</button><button class="btn ghost sm wc-no">Reject</button></div>`);
    card.querySelector(".wc-orig").textContent = s.text;
    card.querySelector(".wc-x").onclick = closeCard;
    card.querySelector(".wc-no").onclick = closeCard;
    document.body.append(card);
    let res;
    try { res = await API().WriteAssist(s.text, mode, arg); } catch (e) { res = { error: "Blaze couldn't do that right now. Nothing was changed." }; }
    if (id !== req || !card) return; // rejected or replaced meanwhile
    const nw = card.querySelector(".wc-new"); nw.classList.remove("wc-wait");
    const again = card.querySelector(".wc-again"); again.disabled = false; again.onclick = () => { sel = s; run(mode, arg); };
    if (!res || res.error) { nw.textContent = (res && res.error) || "Nothing came back. Nothing was changed."; nw.classList.add("bad"); return; }
    nw.textContent = res.text;
    if (res.note) { const n = card.querySelector(".wc-note"); n.textContent = res.note; n.hidden = false; }
    const ok = card.querySelector(".wc-ok"), cp = card.querySelector(".wc-copy");
    ok.disabled = cp.disabled = false;
    cp.onclick = async () => { await copy(res.text); toast("Copied", "ok"); };
    ok.onclick = () => accept(s, res.text);
  }

  async function copy(t) {
    try { if (window.runtime && window.runtime.ClipboardSetText) { if (await window.runtime.ClipboardSetText(t)) return true; } } catch (e) {}
    try { await navigator.clipboard.writeText(t); return true; } catch (e) { return false; }
  }

  async function accept(s, text) {
    if (s.source === "reply") {
      const done = await copy(text);
      closeCard();
      toast(done ? "Copied. Paste it back into your document." : "Couldn't copy. Select the new text and copy it.", done ? "ok" : "bad");
      return;
    }
    const ta = input(); if (!ta) return closeCard();
    const before = ta.value;
    let start = s.start, end = s.end;
    if (before.slice(start, end) !== s.text) { // the box changed meanwhile: find the same text again
      const i = before.indexOf(s.text);
      if (i < 0) { await copy(text); closeCard(); toast("The selected text changed, so nothing was replaced. The new version is copied.", "warn"); return; }
      start = i; end = i + s.text.length;
    }
    ta.value = before.slice(0, start) + text + before.slice(end);
    ta.setSelectionRange(start, start + text.length); ta.focus();
    ta.dispatchEvent(new Event("input", { bubbles: true }));
    closeCard();
    const u = h("div", "wundo", `Replaced. <button class="btn ghost xs">Undo</button>`);
    u.querySelector("button").onclick = () => { if (ta.value.slice(start, start + text.length) === text) { ta.value = before; ta.dispatchEvent(new Event("input", { bubbles: true })); } u.remove(); };
    document.body.append(u); setTimeout(() => u.remove(), 8000);
  }

  document.addEventListener("mouseup", () => setTimeout(showBar, 0));
  document.addEventListener("keyup", e => { if (e.shiftKey || e.key === "Shift" || (e.ctrlKey && e.key === "a")) setTimeout(showBar, 0); });
  document.addEventListener("keydown", e => { if (e.key === "Escape") { if (card) closeCard(); hideBar(); } });
  document.addEventListener("selectionchange", () => { if (bar && !current()) hideBar(); });
  loadMenu();
  window.Writing = { run, accept, current, state: () => ({ sel, card: !!card }) };
})();
