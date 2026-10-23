// Specialists (v0.6.0). The lead (the main chat) splits a big task and briefs
// specialists that run in parallel. They show in one compact strip docked above the
// composer instead of a card each: a row per specialist (mini orb, name, task, state,
// time, Stop), a live line with its current step, and on expand its plan, its log, its
// report and, once finished, the files it changed with Apply / Discard (the lead
// normally reviews and applies itself). Background agents keep their cards (agents.js).
const Specs = (() => {
  const recs = {}; // handle -> { v, steps: [], el, open, line }
  let strip = null, folded = false, sig = "";
  const active = s => s === "queued" || s === "running" || s === "waiting";
  const two = n => String(n).padStart(2, "0");
  const clock = secs => { secs = Math.max(0, Math.round(secs || 0)); return secs >= 3600 ? `${Math.floor(secs / 3600)}:${two(Math.floor(secs / 60) % 60)}:${two(secs % 60)}` : `${Math.floor(secs / 60)}:${two(secs % 60)}`; };
  const elapsed = v => v.started ? (v.ended ? (v.ended - v.started) / 1000 : (Date.now() - v.started) / 1000 + (v.skew || 0)) : 0;
  const plainLine = s => (typeof friendly === "function" ? friendly(s.name, s.args) : "") || s.plain || "Working on it";
  const plainNow = t => window.STEPS && STEPS.plainStatus ? (STEPS.plainStatus(t) || "") : (t || "");
  const has = h => !!recs[h];
  const claims = v => !!v && (v.kind === "specialist" || has(v.handle));
  const rec = h => recs[h] || (recs[h] = { v: { handle: h, status: "queued", n: 0, kind: "specialist" }, steps: [], el: null, open: false });
  // Finished = nothing left to do or decide (not running, queued, waiting or awaiting review).
  const finished = r => !active(r.v.status) && r.v.review !== "pending";
  const mine = () => Object.values(recs).filter(r => r.v.conv && r.v.conv === convId && !r.gone);
  let hideTimer = null;
  const HIDE_AFTER = 6000; // a finished round stays this long, then the strip tucks away
  function clearFinished() { mine().forEach(r => { if (finished(r)) { r.gone = true; r.open = false; } }); render(true); }

  function stateWord(v) {
    const st = v.status;
    if (st === "done" && v.review === "pending") return "Review";
    if (v.review === "applied") return "Applied";
    if (v.review === "discarded") return "Discarded";
    return { queued: v.queue ? `Queued · #${v.queue}` : "Queued", running: "Running", waiting: "Needs you", done: "Done", failed: "Failed", cancelled: "Stopped", interrupted: "Interrupted" }[st] || st;
  }

  function ensureStrip() {
    const wrap = typeof pane !== "undefined" && pane ? $(".cwrap", pane) : null;
    if (!wrap) return null;
    if (strip && wrap.contains(strip)) return strip; // v0.19: the pane is often off-page (Home), so not isConnected
    strip = h("div", "specs");
    strip.id = "specs";
    strip.innerHTML = `<button class="sp-hd" title="Show or hide the specialists"><span class="label ink">Specialists</span><span class="label sp-sum"></span><span class="grow"></span><span class="label sp-clear" role="button" tabindex="0" title="Hide the finished specialists">Clear</span><svg class="i sm chev" viewBox="0 0 16 16">${IC.down}</svg></button><div class="sp-list"></div>`;
    $(".sp-hd", strip).onclick = () => { folded = !folded; render(true); };
    const clr = $(".sp-clear", strip);
    clr.onclick = e => { e.stopPropagation(); clearFinished(); };
    clr.onkeydown = e => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); e.stopPropagation(); clearFinished(); } };
    wrap.insertBefore(strip, $(".inbox", wrap));
    return strip;
  }

  function buildRow(r) {
    const el = h("div", "sprow");
    el.dataset.h = r.v.handle;
    el.innerHTML = `<div class="sp-line" role="button" tabindex="0"><span class="sp-mk"></span><span class="sp-name"></span><span class="sp-task"></span><span class="sp-mode label"></span><span class="sp-state label"></span><span class="sp-time mono"></span>
        <button class="btn ghost xs sp-stop" title="Stop this specialist">${icon("stop", "i sm")}<span>Stop</span></button><svg class="i sm chev" viewBox="0 0 16 16">${IC.down}</svg></div>
      <div class="sp-now"><span class="sp-wt"></span></div>
      <div class="sp-ask"></div>
      <div class="sp-body"></div>`;
    const line = $(".sp-line", el);
    line.onclick = e => {
      if (e.target.closest(".sp-stop")) return;
      r.open = !r.open; draw(r);
      const list = el.parentElement;
      if (r.open && list) list.scrollTop = el.offsetTop - list.offsetTop;
      if (typeof scrollDown === "function") scrollDown();
    };
    line.onkeydown = e => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); line.click(); } };
    $(".sp-stop", el).onclick = e => { e.stopPropagation(); API().AgentStop(r.v.handle); };
    r.el = el;
    return el;
  }

  function draw(r) {
    if (!r.el) return;
    const el = r.el, v = r.v, st = v.status, live = active(st), n = v.n || r.steps.length;
    el.className = "sprow " + st + (live && st !== "queued" ? " live" : "") + (r.open ? " open" : "") + (v.review ? " rv-" + v.review : "");
    $(".sp-name", el).textContent = v.handle;
    $(".sp-task", el).textContent = v.task || ""; $(".sp-task", el).title = v.task || "";
    $(".sp-mode", el).textContent = (v.mode || "build") + (v.iso ? " · " + v.iso : "");
    $(".sp-mode", el).title = v.iso === "worktree" ? `Its own git worktree on branch ${v.branch || ""}` : v.iso === "copy" ? "Its own copy of the folder" : v.mode === "plan" ? "Read and scout only" : "";
    $(".sp-state", el).textContent = stateWord(v);
    $(".sp-time", el).textContent = v.started ? clock(elapsed(v)) : "";
    $(".sp-stop", el).style.display = live ? "" : "none";
    const steps = `${n} step${n === 1 ? "" : "s"}`;
    let line;
    if (live) line = st === "queued" ? "Waits for a free slot" : st === "waiting" ? (v.now || "Waiting for you") : (r.livePlain || plainNow(v.now) || "Starting");
    else if (v.review === "pending") line = `${steps} · changed ${(v.changes || []).length} file${(v.changes || []).length === 1 ? "" : "s"} · the lead reviews before applying`;
    else line = ({ done: "Done", failed: "Failed", cancelled: "Stopped", interrupted: "Interrupted" }[st] || "") + " · " + steps;
    if (r.line !== line) {
      r.line = line;
      const wt = $(".sp-wt", el), old = $$(".phrase", wt), span = h("span", "phrase" + (live && st !== "queued" ? " shim in" : ""), esc(line));
      wt.append(span);
      requestAnimationFrame(() => span.classList.remove("in"));
      old.forEach(o => { o.classList.add("out"); setTimeout(() => o.remove(), 260); });
    }
    const body = $(".sp-body", el);
    body.style.display = r.open ? "" : "none";
    if (r.open) {
      body.innerHTML = "";
      // Finished: what needs a decision first (changes + Apply / Discard), then its report.
      const acts = h("div", "sp-acts");
      if (!live && v.review === "pending") {
        const ap = h("button", "btn accent sm", "Apply changes"); ap.onclick = async () => { const e = await API().SpecialistReview(v.handle, "apply"); if (e) toastErr(e); };
        const dc = h("button", "btn line sm", "Discard"); dc.onclick = async () => { const e = await API().SpecialistReview(v.handle, "discard"); if (e) toastErr(e); };
        acts.append(h("span", "sp-note", "Nothing reached your folder yet."), h("span", "grow"), ap, dc);
      }
      if (st === "interrupted") {
        const res = h("button", "btn accent sm", `${icon("retry", "i sm")}<span>Resume</span>`); res.onclick = () => API().AgentResume(v.handle);
        const dis = h("button", "btn line sm", "Discard"); dis.onclick = async () => { await API().AgentDiscard(v.handle); remove(v.handle); };
        acts.append(h("span", "sp-note", "Blaze closed while it was working."), h("span", "grow"), res, dis);
      }
      if ((v.changes || []).length) {
        const ch = h("div", "sp-sec sp-changes");
        ch.innerHTML = `<div class="label">Changed in its copy</div>` + v.changes.slice(0, 20).map(c => `<div class="sp-file"><span class="op label ${c.op}">${c.op === "added" ? "A" : c.op === "deleted" ? "D" : "M"}</span><span class="mono">${esc(c.path)}</span></div>`).join("") + (v.changes.length > 20 ? `<div class="label weak">+${v.changes.length - 20} more</div>` : "");
        body.append(ch);
      }
      if (acts.children.length) body.append(acts);
      const rep = st === "done" ? v.result : (v.error || "") + (v.result ? "\n\n" + v.result : "");
      if (!live && rep) { const d = h("div", "sp-sec sp-report md"); d.innerHTML = `<div class="label">${st === "done" ? "Report" : "What went wrong"}</div>` + MD.render(rep); body.append(d); }
      if (v.plan) body.append(h("div", "sp-sec", `<div class="label">Plan</div><div class="sp-plan">${esc(v.plan)}</div>`));
      if (v.path) body.append(h("div", "sp-sec sp-where", `<span class="label">Works in</span><span class="mono">${esc(v.path)}</span>${v.branch ? `<span class="label weak">${esc(v.branch)}</span>` : ""}`));
      const log = h("div", "sp-log");
      const t = { stepsEl: log, steps: {}, order: [], open: true, expanded: true };
      r.steps.forEach((s, i) => { const row = addStep(t, (s.id || "s") + "_" + i, s.name, s.args); if (s.done) finishStep(t, row, s.result); else row.classList.add("run"); });
      if (!r.steps.length) log.append(h("div", "sp-empty label weak", live ? "No steps yet" : "No tool steps"));
      body.append(log);
    }
    if (!live) $(".sp-ask", el).innerHTML = "";
  }

  function toastErr(text) { if (typeof toast === "function") toast(text, "err"); }

  // Redraws the strip for the open chat. Cheap when nothing changed (a signature check).
  function render(force) {
    const list = mine();
    const s = ensureStrip();
    if (!s) return;
    if (!list.length) { s.style.display = "none"; sig = ""; clearTimeout(hideTimer); hideTimer = null; return; }
    s.style.display = "";
    // When the whole round is finished, show the outcome briefly, then hide it, unless a
    // row is open (being read). Anything still running or awaiting review keeps it up.
    const allDone = list.every(finished);
    s.classList.toggle("all-done", allDone);
    if (allDone && !list.some(r => r.open)) {
      if (!hideTimer) hideTimer = setTimeout(() => { hideTimer = null; if (mine().every(finished) && !mine().some(r => r.open)) clearFinished(); }, HIDE_AFTER);
    } else if (hideTimer) { clearTimeout(hideTimer); hideTimer = null; }
    s.classList.toggle("folded", folded);
    const c = { run: 0, q: 0, wait: 0, rev: 0, done: 0 };
    list.forEach(r => { const st = r.v.status; if (st === "running") c.run++; else if (st === "queued") c.q++; else if (st === "waiting") c.wait++; else if (r.v.review === "pending") c.rev++; else c.done++; });
    const parts = [];
    if (c.run) parts.push(`${c.run} running`); if (c.wait) parts.push(`${c.wait} need${c.wait === 1 ? "s" : ""} you`); if (c.q) parts.push(`${c.q} queued`); if (c.rev) parts.push(`${c.rev} to review`); if (c.done) parts.push(`${c.done} done`);
    const sum = $(".sp-sum", s); sum.textContent = parts.join(" · "); sum.classList.toggle("acc", c.run + c.wait > 0);
    const key = list.map(r => r.v.handle).join("|");
    const listEl = $(".sp-list", s);
    if (force || key !== sig) {
      sig = key;
      listEl.innerHTML = "";
      list.forEach(r => { if (!r.el) buildRow(r); listEl.append(r.el); draw(r); });
    }
    if (typeof Agents !== "undefined" && Agents.orbBadge) Agents.orbBadge();
  }

  function upsert(v) {
    if (!v || !v.handle) return;
    const r = rec(v.handle);
    if (v.started && !v.ended) v.skew = (v.seconds || 0) - (Date.now() - v.started) / 1000;
    r.v = { ...r.v, ...v };
    if (active(r.v.status) || r.v.review === "pending") r.gone = false; // resumed or new work shows again
    if (v.steps) r.steps = v.steps.slice();
    render();
    draw(r);
  }

  function step(d) {
    const r = rec(d.handle), s = d.step;
    const i = r.steps.findIndex(x => x.id === s.id && x.at === s.at);
    if (i >= 0) r.steps[i] = s; else r.steps.push(s);
    if (!s.done) r.livePlain = plainLine(s);
    r.v.n = Math.max(r.v.n || 0, r.steps.length);
    draw(r);
  }

  function remove(handle) { const r = recs[handle]; if (r && r.el) r.el.remove(); delete recs[handle]; render(true); }

  // A saved chat's specialists (from Agents.load).
  function load(list) {
    // A reopened chat shows only specialists that still need something; finished ones stay hidden.
    (list || []).forEach(v => { const r = rec(v.handle); r.v = { ...v }; r.steps = (v.steps || []).slice(); r.line = null; r.gone = finished(r); });
    render(true);
  }

  // Approvals and questions from a specialist land on its row (opened).
  function askCard(handle, card) {
    const r = rec(handle); render(true);
    if (!r.el) return false;
    folded = false; render(true);
    $(".sp-ask", r.el).append(card);
    return true;
  }
  function confirm(c) {
    const btns = c.always
      ? `<button class="btn accent sm yes">Allow once</button><button class="btn line sm al">Always allow</button><button class="btn ghost sm no">Don't allow</button>`
      : `<button class="btn accent sm yes">Yes, do it</button><button class="btn line sm no">No</button>`;
    const card = h("div", "ag-q", `<div class="label acc"></div><div class="detail"></div><div class="actions">${btns}</div>`);
    $(".label", card).textContent = c.title; $(".detail", card).textContent = c.detail || "";
    if (!c.detail) $(".detail", card).remove();
    const done = choice => { API().AnswerChoice(c.id, choice); card.remove(); };
    $(".yes", card).onclick = () => done("once"); $(".no", card).onclick = () => done("no");
    if (c.always) $(".al", card).onclick = () => done("always");
    return askCard(c.agent, card);
  }
  function question(q) {
    const card = h("div", "ag-q", `<div class="label acc">Question</div><div class="qtext"></div><div class="actions"></div><div class="ag-ans"><input class="field" placeholder="Type an answer…"><button class="btn line sm">Answer</button></div>`);
    $(".qtext", card).textContent = q.question;
    const send = t => { if (!String(t || "").trim()) return; API().AnswerAgent(q.id, t); card.remove(); };
    (q.choices || []).forEach(c => { const b = h("button", "btn line sm", esc(c)); b.onclick = () => send(c); $(".actions", card).append(b); });
    if (!(q.choices || []).length) $(".actions", card).remove();
    const inp = $("input", card);
    inp.onkeydown = e => { if (e.key === "Enter") { e.preventDefault(); send(inp.value); } };
    $(".ag-ans button", card).onclick = () => send(inp.value);
    return askCard(q.agent, card);
  }

  function counts() {
    let running = 0, queued = 0, waiting = 0;
    Object.values(recs).forEach(r => { const s = r.v.status; if (s === "running") running++; else if (s === "queued") queued++; else if (s === "waiting") waiting++; });
    return { running, queued, waiting };
  }

  // Elapsed time ticks once a second; the strip follows the open chat (new chat, switch).
  let lastConv = null;
  setInterval(() => {
    if (lastConv !== convId) { lastConv = convId; render(true); }
    mine().forEach(r => { if (r.el && active(r.v.status) && r.v.started) { const t = $(".sp-time", r.el); if (t) t.textContent = clock(elapsed(r.v)); } });
  }, 1000);

  return { has, claims, upsert, step, remove, load, confirm, question, counts, render, recs, active };
})();
