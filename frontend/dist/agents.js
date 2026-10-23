// Background agents (v0.4.7). The main chat hands long work to sub-agents; each one
// gets a live card in the chat: its handle and task, the current plain step, elapsed
// time and Stop. Finished cards collapse to "Done · N steps" and expand to the log and
// the agent's report. Approvals and questions from an agent show on its card, so the
// main chat never blocks. The orb shows a compact running-agents indicator.
const Agents = (() => {
  const MAIN = ["dispatch_agent", "message_agent", "cancel_agent", "list_agents", "dispatch_specialist", "check_specialist", "message_specialist", "cancel_specialist", "apply_specialist"];
  // Specialists (v0.6.0) live in their own strip above the composer (specialists.js).
  const spec = v => typeof Specs !== "undefined" && Specs.claims(v);
  const recs = {}; // handle -> { v, steps: [], el, open }
  const isMainTool = n => MAIN.includes(n);
  const active = s => s === "queued" || s === "running" || s === "waiting";
  const two = n => String(n).padStart(2, "0");
  const clock = secs => { secs = Math.max(0, Math.round(secs || 0)); return secs >= 3600 ? `${Math.floor(secs / 3600)}:${two(Math.floor(secs / 60) % 60)}:${two(secs % 60)}` : `${Math.floor(secs / 60)}:${two(secs % 60)}`; };
  const elapsed = v => v.started ? (v.ended ? (v.ended - v.started) / 1000 : (Date.now() - v.started) / 1000 + (v.skew || 0)) : 0;
  const plainLine = s => (typeof friendly === "function" ? friendly(s.name, s.args) : "") || s.plain || "Working on it";
  const plainNow = t => window.STEPS && STEPS.plainStatus ? (STEPS.plainStatus(t) || "") : (t || "");

  function rec(handle) { return recs[handle] || (recs[handle] = { v: { handle, status: "queued", n: 0 }, steps: [], el: null, open: false }); }

  function build(r) {
    const el = h("div", "agcard");
    el.dataset.h = r.v.handle;
    el.innerHTML = `<div class="ag-hd"><span class="ag-sq"></span><span class="label ink ag-name"></span><span class="label ag-state"></span><span class="grow"></span><span class="ag-time mono"></span>
        <button class="btn ghost xs ag-stop" title="Stop this agent">${icon("stop", "i sm")}<span>Stop</span></button></div>
      <div class="ag-task"></div>
      <button class="ag-now"><span class="ag-wt"></span><svg class="i sm chev" viewBox="0 0 16 16">${IC.down}</svg></button>
      <div class="ag-ask"></div>
      <div class="ag-body"><div class="ag-log"></div><div class="ag-report md"></div></div>
      <div class="ag-acts"></div>`;
    $(".ag-stop", el).onclick = e => { e.stopPropagation(); API().AgentStop(r.v.handle); };
    $(".ag-now", el).onclick = () => { if (active(r.v.status)) return; r.open = !r.open; draw(r); };
    r.el = el;
    draw(r);
    return el;
  }

  function draw(r) {
    const el = r.el, v = r.v; if (!el) { orbBadge(); return; }
    const st = v.status, live = active(st), n = v.n || r.steps.length;
    el.className = "agcard " + st + (r.open ? " open" : "");
    $(".ag-name", el).textContent = "Agent · " + v.handle;
    $(".ag-state", el).textContent = { queued: v.queue ? `Queued · #${v.queue}` : "Queued", running: "Running", waiting: "Needs you", done: "Done", failed: "Failed", cancelled: "Stopped", interrupted: "Interrupted" }[st] || st;
    $(".ag-task", el).textContent = v.task || "";
    $(".ag-task", el).title = v.task || "";
    $(".ag-time", el).textContent = v.started ? clock(elapsed(v)) : "";
    $(".ag-stop", el).style.display = live ? "" : "none";
    const wt = $(".ag-wt", el), steps = `${n} step${n === 1 ? "" : "s"}`;
    let line;
    // Live: the plain words for its current step (never a raw command); a wait says so.
    if (live) line = st === "queued" ? "Waits for a free slot" : st === "waiting" ? (v.now || "Waiting for you") : (r.livePlain || plainNow(v.now) || "Starting");
    else line = { done: `Done · ${steps}`, failed: `Failed · ${steps}`, cancelled: `Stopped · ${steps}`, interrupted: `Interrupted · ${steps}` }[st] || steps;
    if (r.line !== line) {
      r.line = line;
      const old = $$(".phrase", wt), span = h("span", "phrase" + (live && st !== "queued" ? " shim in" : ""), esc(line));
      wt.append(span);
      requestAnimationFrame(() => span.classList.remove("in"));
      old.forEach(o => { o.classList.add("out"); setTimeout(() => o.remove(), 260); });
    }
    $(".ag-now", el).title = live ? "" : `${steps} · click to ${r.open ? "hide" : "see"} the log`;
    // The log and the report show only when a finished card is open.
    const body = $(".ag-body", el);
    body.style.display = !live && r.open ? "" : "none";
    if (!live && r.open) {
      // The same rows as the main chat's steps: plain words, the exact command on expand.
      const log = $(".ag-log", el); log.innerHTML = "";
      const t = { stepsEl: log, steps: {}, order: [], open: true, expanded: true };
      r.steps.forEach((s, i) => { const row = addStep(t, (s.id || "s") + "_" + i, s.name, s.args); if (s.done) finishStep(t, row, s.result); });
      if (!r.steps.length) log.append(h("div", "ag-empty label weak", "No tool steps"));
      const rep = v.status === "done" ? v.result : (v.error || "") + (v.result ? "\n\n" + v.result : "");
      $(".ag-report", el).innerHTML = rep ? `<div class="label">${v.status === "done" ? "Report" : "What went wrong"}</div>` + MD.render(rep) : "";
    }
    const acts = $(".ag-acts", el); acts.innerHTML = "";
    if (st === "interrupted") {
      const note = h("span", "ag-note", "Blaze closed while this agent was working.");
      const res = h("button", "btn accent sm", `${icon("retry", "i sm")}<span>Resume</span>`); res.onclick = () => API().AgentResume(v.handle);
      const dis = h("button", "btn line sm", "Discard"); dis.onclick = async () => { await API().AgentDiscard(v.handle); remove(v.handle); };
      acts.append(note, h("span", "grow"), res, dis);
    }
    acts.style.display = acts.children.length ? "" : "none";
    if (!live) $(".ag-ask", el).innerHTML = "";
    orbBadge();
  }

  // Put a card in the chat log (at the end, or after a given element).
  function place(handle, col, after) {
    const r = rec(handle);
    if (!r.el) build(r);
    if (after && after.parentNode === col) after.after(r.el); else col.append(r.el);
    return r.el;
  }

  function upsert(v) {
    if (!v || !v.handle) return;
    if (spec(v)) return Specs.upsert(v);
    const r = rec(v.handle);
    if (v.started && !v.ended) v.skew = (v.seconds || 0) - (Date.now() - v.started) / 1000; // backend clock vs ours
    r.v = { ...r.v, ...v };
    if (v.steps) r.steps = v.steps.slice();
    if (!r.el && typeof pane !== "undefined" && pane && v.conv && v.conv === convId) {
      const col = $("#log .col", pane); if (col) { place(v.handle, col); if (typeof scrollDown === "function") scrollDown(); }
    }
    draw(r);
  }

  function step(d) {
    if (spec({ handle: d.handle })) return Specs.step(d);
    const r = rec(d.handle), s = d.step;
    const i = r.steps.findIndex(x => x.id === s.id && x.at === s.at);
    if (i >= 0) r.steps[i] = s; else r.steps.push(s);
    if (!s.done) r.livePlain = plainLine(s);
    r.v.n = Math.max(r.v.n || 0, r.steps.length);
    draw(r);
  }

  function remove(handle) { if (spec({ handle })) return Specs.remove(handle); const r = recs[handle]; if (r && r.el) r.el.remove(); delete recs[handle]; orbBadge(); }

  // Cards for a saved chat: each after the turn that dispatched it.
  function load(list, col, anchors) {
    if (typeof Specs !== "undefined") { Specs.load((list || []).filter(v => v.kind === "specialist")); list = (list || []).filter(v => v.kind !== "specialist"); }
    (list || []).forEach(v => {
      const r = rec(v.handle);
      r.v = { ...v }; r.steps = (v.steps || []).slice(); r.el = null; r.line = null;
      place(v.handle, col, anchors && anchors[v.handle]);
    });
    orbBadge();
  }

  // An approval card from a sub-agent shows on its card.
  function confirm(c) {
    if (spec({ handle: c.agent })) return Specs.confirm(c);
    const r = rec(c.agent);
    if (!r.el) { const col = $("#log .col", pane); if (col) place(c.agent, col); }
    if (!r.el) return false;
    const btns = c.always
      ? `<button class="btn accent sm yes">Allow once</button><button class="btn line sm al">Always allow</button><button class="btn ghost sm no">Don't allow</button>`
      : `<button class="btn accent sm yes">Yes, do it</button><button class="btn line sm no">No</button>`;
    const card = h("div", "ag-q", `<div class="label acc"></div><div class="detail"></div><div class="actions">${btns}</div>`);
    $(".label", card).textContent = c.title; $(".detail", card).textContent = c.detail || "";
    if (!c.detail) $(".detail", card).remove();
    const done = choice => { API().AnswerChoice(c.id, choice); card.remove(); };
    $(".yes", card).onclick = () => done("once"); $(".no", card).onclick = () => done("no");
    if (c.always) $(".al", card).onclick = () => done("always");
    $(".ag-ask", r.el).append(card);
    if (typeof scrollDown === "function") scrollDown();
    return true;
  }

  // A question from a sub-agent (ask_user): choices and a free answer.
  function question(q) {
    if (spec({ handle: q.agent })) return Specs.question(q);
    const r = rec(q.agent);
    if (!r.el) { const col = $("#log .col", pane); if (col) place(q.agent, col); }
    if (!r.el) return;
    const card = h("div", "ag-q", `<div class="label acc">Question</div><div class="qtext"></div><div class="actions"></div><div class="ag-ans"><input class="field" placeholder="Type an answer…"><button class="btn line sm">Answer</button></div>`);
    $(".qtext", card).textContent = q.question;
    const send = t => { if (!String(t || "").trim()) return; API().AnswerAgent(q.id, t); card.remove(); };
    (q.choices || []).forEach(c => { const b = h("button", "btn line sm", esc(c)); b.onclick = () => send(c); $(".actions", card).append(b); });
    if (!(q.choices || []).length) $(".actions", card).remove();
    const inp = $("input", card);
    inp.onkeydown = e => { if (e.key === "Enter") { e.preventDefault(); send(inp.value); } };
    $(".ag-ans button", card).onclick = () => send(inp.value);
    $(".ag-ask", r.el).append(card);
    if (typeof scrollDown === "function") scrollDown();
  }

  function counts() {
    let running = 0, queued = 0, waiting = 0;
    Object.values(recs).forEach(r => { const s = r.v.status; if (s === "running") running++; else if (s === "queued") queued++; else if (s === "waiting") waiting++; });
    if (typeof Specs !== "undefined") { const c = Specs.counts(); running += c.running; queued += c.queued; waiting += c.waiting; }
    return { running, queued, waiting, total: running + queued + waiting };
  }

  // The orb's compact indicator: "2 AGENTS" (+ "1 NEEDS YOU").
  function orbBadge() {
    const bar = document.getElementById("orbbar"); if (!bar) return;
    let b = $(".orbag", bar);
    if (!b) { b = h("button", "orbag"); b.title = "Background agents"; b.onclick = () => API().ExpandOrb(); const g = $(".grow", bar); bar.insertBefore(b, g ? g.nextSibling : null); }
    const c = counts();
    b.style.display = c.total ? "" : "none";
    b.classList.toggle("wait", c.waiting > 0);
    const n = c.running + c.waiting;
    const k = n || c.queued;
    b.title = `${n} running${c.queued ? `, ${c.queued} queued` : ""}${c.waiting ? `, ${c.waiting} need${c.waiting === 1 ? "s" : ""} you` : ""} · open the full window`;
    b.innerHTML = `<span class="ag-sq"></span><span class="label ${c.waiting ? "acc" : "ink"}">${k} agent${k === 1 ? "" : "s"}</span>${c.queued && n ? `<span class="label weak">+${c.queued}</span>` : ""}`;
  }

  // Elapsed time ticks once a second on live cards.
  setInterval(() => Object.values(recs).forEach(r => { if (r.el && active(r.v.status) && r.v.started) { const t = $(".ag-time", r.el); if (t) t.textContent = clock(elapsed(r.v)); } }), 1000);

  on("agent", upsert);
  on("agent_step", step);
  on("agent_removed", remove);
  on("agent_question", question);

  return { isMainTool, upsert, load, place, confirm, question, counts, orbBadge, recs, active };
})();
