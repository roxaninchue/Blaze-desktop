// Notifications, proactive suggestions and screen vision (notify_app.go, watch.go, vision_app.go).
// Loaded after app.js; it only uses app.js globals (API, on, $, $$, h, esc, icon, group, pad, S,
// show, openChat, sendText, current, pane) and the SECTIONS / SETTINGS_FNS hooks.
IC.bell = IC.bell || '<path d="M4 11.5V7a4 4 0 0 1 8 0v4.5l1 1H3zM6.5 14h3"/>';
IC.eye = IC.eye || '<path d="M1.5 8S4 3.5 8 3.5 14.5 8 14.5 8 12 12.5 8 12.5 1.5 8 1.5 8z"/><rect x="6.5" y="6.5" width="3" height="3"/>';

// ---------- privacy indicator ----------
// Shown whenever Blaze reads the screen (text or pixels), for at least a moment so a
// fast cached read is still visible. "Turn off" switches screen reading off.
const SCR = { on: false, since: 0, hideT: 0, how: "" };
function screenIndicator(on, how) {
  let el = $("#screenind");
  if (!el) {
    el = h("div", "", `<span class="sq"></span><span class="label ink">Reading screen</span><span class="label weak how"></span><button class="btn ghost xs" title="Turn screen reading off">Turn off</button>`);
    el.id = "screenind"; el.setAttribute("role", "status"); el.setAttribute("aria-live", "polite");
    el.style.setProperty("--wails-draggable", "no-drag");
    $("button", el).onclick = async () => {
      const p = { ...((S && S.vision) || {}), off: true };
      const v = await API().SetVisionPrefs(p); if (S) S.vision = (v && v.prefs) || p;
      el.classList.remove("on"); toastNote("Screen reading is off. Turn it on in Settings › Notifications.");
    };
    document.body.append(el);
  }
  clearTimeout(SCR.hideT);
  if (on) {
    // In the full window it sits in the top bar (never over the chat); in the orb it floats under the orb bar.
    const top = $("#top"), orb = document.body.classList.contains("orb");
    if (!orb && top && $("#route", top)) { if (el.parentNode !== top) top.insertBefore(el, $("#route", top)); el.classList.add("docked"); }
    else { if (el.parentNode !== document.body) document.body.append(el); el.classList.remove("docked"); }
    SCR.on = true; SCR.since = Date.now(); SCR.how = how || "";
    $(".how", el).textContent = how === "pixels" ? "Screenshot" : "Text";
    el.classList.add("on");
  } else {
    const left = Math.max(0, 1500 - (Date.now() - SCR.since));
    SCR.hideT = setTimeout(() => { SCR.on = false; el.classList.remove("on"); }, left);
  }
}
on("screen", d => screenIndicator(!!(d && d.on), d && d.how));
on("vision", p => { if (S) S.vision = p; });

// A small transient note (top right), for confirmations outside a chat.
function toastNote(text) {
  const t = h("div", "ntoast", `<span class="sq"></span><span>${esc(text)}</span>`);
  document.body.append(t);
  setTimeout(() => t.classList.add("in"), 10);
  setTimeout(() => { t.classList.remove("in"); setTimeout(() => t.remove(), 300); }, 3600);
}

// ---------- clicks on notifications ----------
on("open_chat", id => { if (id) openChat(id); });
// A suggestion the user clicked: show it as a card with "Help me" / "Not now".
on("suggest", e => {
  if (!e) return;
  if (view !== "work") show("work");
  const card = h("div", "card confirm suggestcard", `<div class="title"><span class="label acc">${icon("sparks", "i sm")}Suggestion</span></div>
    <div class="st">${esc(e.title)}</div><div class="detail">${esc(e.body)}</div>
    <div class="actions"><button class="btn accent sm yes">Help me</button><button class="btn line sm no">Not now</button></div>`);
  const done = yes => { API().SuggestionAction(e.id, yes); card.classList.add("done"); $(".actions", card).innerHTML = `<span class="label">${yes ? "On it" : "Dismissed"}</span>`; };
  $(".yes", card).onclick = () => { done(true); if (!busy) sendText(e.prompt || e.title, []); };
  $(".no", card).onclick = () => done(false);
  const col = $("#log .col", pane); const heroEl = $(".hero", col); if (heroEl) heroEl.remove();
  col.append(card); scrollDown(true);
});

// ---------- Settings › Notifications ----------
SECTIONS.splice(Math.max(0, SECTIONS.findIndex(s => s[0] === "appearance")), 0, ["notifications", "Notifications", "bell"]);
window.SETTINGS_FNS = window.SETTINGS_FNS || {};
window.SETTINGS_FNS.notifications = async (col, redraw) => {
  col.append(h("p", "lead", "Blaze tells you when something needs you, and can look at your screen when you ask about what's on it. Everything here stays on this computer."));
  col.lastChild.style.marginBottom = "20px";
  const [nv, vv] = await Promise.all([API().NotifyStatus(), API().VisionStatus()]);
  const n = nv || {}, p = { ...(n.prefs || {}) };
  const save = async () => { const r = await API().SetNotifyPrefs(p); if (r) Object.assign(n, r); };
  const tg = (on, title, desc, fn, disabled) => {
    const l = h("label", "toggle" + (disabled ? " dis" : ""), `<input type="checkbox" ${on ? "checked" : ""} ${disabled ? "disabled" : ""}><span><span class="tt">${title}</span><span class="td">${desc}</span></span>`);
    $("input", l).onchange = e => fn(e.target.checked); return l;
  };

  // 01 notifications
  const det = h("button", "btn ghost xs", `${icon("retry", "i sm")}Detect again`);
  const g1 = group("Notifications", "", det);
  const st = h("div", "nstat");
  const drawStat = v => {
    const ok = !!v.method && !v.last_error;
    st.innerHTML = `<span class="sq ${v.prefs && v.prefs.off ? "off" : ok ? "" : "ink"}"></span><div class="main"><div class="t">${v.prefs && v.prefs.off ? "Off" : esc(v.method || "No way to show notifications was found")}</div>
      <div class="s">${v.method ? `${v.kind === "native" ? "Built into your system" : "Through a command"}${v.clicks ? " · clicking one opens Blaze" : ""}${v.fallback && v.fallback !== v.method ? ` · scheduled jobs use ${esc(v.fallback)}` : ""} · checked ${esc(v.probed || "")}, cached in <span class="mono">~/.blaze/notify.json</span>` : esc((v.missing || []).join(" · "))}</div>
      ${v.last_error ? `<div class="s bad">Last error: ${esc(v.last_error)}</div>` : ""}</div>`;
    const tb = h("button", "btn line sm", "Send a test"); const out = h("span", "small sub tout");
    tb.onclick = async () => { tb.disabled = true; tb.textContent = "Sending…"; const err = await API().TestNotify(); tb.disabled = false; tb.textContent = "Send a test"; out.innerHTML = err ? `<span class="bad">${esc(err)}</span>` : "Sent. Didn't see it? Check your system's notification settings."; };
    const a = h("div", "acts"); a.append(tb, out); st.append(a);
  };
  drawStat(n);
  det.onclick = async () => { det.textContent = "Detecting…"; const v = await API().ProbeNotify(); Object.assign(n, v || {}); det.innerHTML = `${icon("retry", "i sm")}Detect again`; drawStat(n); };
  const bx1 = h("div", "box pad formgrid");
  bx1.append(
    tg(!p.off, "Show notifications", "Only for things that matter: a finished task while Blaze is hidden, a failed job, a reminder you asked for.", v => { p.off = !v; save().then(() => drawStat(n)); }),
    tg(!p.no_tasks, "When a chat or background job finishes", "Only when Blaze's window is hidden and the work took a while.", v => { p.no_tasks = !v; save(); }),
    tg(!p.no_jobs, "When a scheduled job runs", "Failures always show; a job that worked waits out quiet hours.", v => { p.no_jobs = !v; save(); }),
  );
  g1.append(st, bx1);

  // 02 proactive suggestions
  const ck = h("button", "btn ghost xs", "Check now");
  const g2 = group("Proactive suggestions", "Off by default. When on, Blaze glances at the front window now and then and speaks up only when it can genuinely help: an error, a failing build, a confusing form. Most checks end in silence.", ck);
  const bx2 = h("div", "box pad formgrid");
  const ws = h("div", "nstat slim");
  const drawWatch = s => { ws.innerHTML = `<span class="sq ${p.proactive && !p.off ? "" : "off"}"></span><div class="main"><div class="s">${esc(s || (p.proactive ? "Waiting for the next check" : "Off"))}</div></div>`; };
  drawWatch(n.watch);
  ck.onclick = async () => { ck.disabled = true; ck.textContent = "Checking…"; drawWatch(await API().CheckSuggestionNow()); ck.disabled = false; ck.textContent = "Check now"; };
  const visionOff = vv && vv.prefs && vv.prefs.off;
  bx2.append(tg(p.proactive, "Suggest help when I might need it", visionOff ? "Needs screen reading, which is off below." : "Uses one short model call per check, at most every few minutes, and never while you're away, in full screen or in quiet hours.", v => { p.proactive = v; save(); drawWatch(v ? "Waiting for the next check" : "Off"); }, false));
  const sel = (label, key, opts, def) => {
    const r = h("div", "nrow", `<span class="tt">${label}</span>`);
    const s = h("select", "field"); s.innerHTML = opts.map(([v, t]) => `<option value="${v}" ${(p[key] || def) == v ? "selected" : ""}>${t}</option>`).join("");
    s.onchange = () => { p[key] = +s.value; save(); }; r.append(s); return r;
  };
  bx2.append(sel("Check at most every", "every", [[5, "5 minutes"], [10, "10 minutes"], [20, "20 minutes"], [30, "30 minutes"], [60, "hour"]], 10),
    sel("At most", "max_hour", [[1, "1 an hour"], [2, "2 an hour"], [3, "3 an hour"], [5, "5 an hour"]], 3));
  const q = n.quiet || ["22:00", "08:00"];
  const qr = h("div", "nrow", `<span class="tt">Quiet hours</span>`);
  // Half-hour steps in 24-hour time (a native time input clips "10:00 PM" and follows the OS locale).
  const times = Array.from({ length: 48 }, (_, i) => `${pad(Math.floor(i / 2))}:${i % 2 ? "30" : "00"}`);
  const tsel = v => { const x = h("select", "field mono qsel"); x.innerHTML = times.concat(times.includes(v) ? [] : [v]).map(t => `<option ${t === v ? "selected" : ""}>${t}</option>`).join(""); return x; };
  const qf = tsel(q[0] || "22:00"), qt = tsel(q[1] || "08:00");
  const qto = h("span", "label weak", "to");
  const qsave = () => { p.quiet_from = qf.value; p.quiet_to = qt.value; save(); };
  qf.onchange = qsave; qt.onchange = qsave;
  const qoff = h("label", "toggle inline", `<input type="checkbox" ${p.no_quiet ? "" : "checked"}><span class="tt">On</span>`);
  const qdis = () => { qf.disabled = qt.disabled = !!p.no_quiet; };
  $("input", qoff).onchange = e => { p.no_quiet = !e.target.checked; qdis(); save(); }; qdis();
  qr.append(qf, qto, qt, qoff);
  bx2.append(qr, ws);
  g2.append(bx2);

  // 03 screen vision
  const g3 = group("Screen vision", "How Blaze reads your screen when you ask about it. Text comes from the accessibility tree; a downscaled screenshot is the fallback. A bar at the top shows whenever the screen is being read.");
  const v = vv || {}; const vp = { ...(v.prefs || {}) }; const caps = v.caps || {};
  const vsave = async () => { const r = await API().SetVisionPrefs(vp); if (S) S.vision = vp; return r; };
  const bx3 = h("div", "box pad formgrid");
  bx3.append(
    tg(!vp.off, "Let Blaze read my screen", "Only the front window that isn't Blaze, only while you're asking or a suggestion check runs. Password fields are never read.", x => { vp.off = !x; vsave(); }),
    tg(!vp.no_pixels, "Allow screenshots", "For windows with little readable text. Downscaled, password fields blurred, never saved to disk. Off: text only.", x => { vp.no_pixels = !x; vsave(); }),
  );
  const mac = v.os === "darwin";
  const cap = (ok, name, via, note, grant) => {
    const r = h("div", "r", `<span class="dot ${ok ? "" : "ink"}"></span><div class="main"><div class="t">${name} <span class="mono small sub">${esc(via || "")}</span></div><div class="s">${esc(note || (ok ? "Ready" : "Not available"))}</div></div>`);
    if (!ok && grant) { const b = h("button", "btn line sm", "Grant access"); b.onclick = grant; r.append(b); }
    return r;
  };
  const rows = h("div", "rows"); rows.style.marginTop = "12px";
  rows.append(cap(caps.text, "Window text", caps.text_via, caps.text_note, mac && caps.text_perm ? () => API().GrantScreenText() : null),
    cap(caps.pixels, "Screenshots", caps.pixels_via, caps.pixels_note, mac && caps.permission ? () => API().GrantScreen() : null));
  const tr = h("div", "keyrow"); tr.style.marginTop = "10px";
  const tb = h("button", "btn line sm", "Read the front window"); const tout = h("span", "small sub tout", v.last ? `Last read: ${esc(v.last)}` : "");
  tb.onclick = async () => { tb.disabled = true; tout.textContent = "Reading…"; tout.textContent = await API().TryReadScreen(); tb.disabled = false; };
  tr.append(tb, tout);
  // Private vision: pictures are read on this computer (the OS's own OCR, sizes and
  // colours) and models get only that text. Covers attachments, screenshots, Copy window.
  const pvb = h("div", "box pad formgrid pvbox"); pvb.style.marginTop = "12px";
  const pvs = h("div", "small sub pvnote");
  const drawPV = pv => {
    pvb.innerHTML = "";
    pvb.append(tg(!!pv.on, "Private vision", "Pictures never leave this computer. Blaze reads them here (text with the OCR built into your OS, plus size and colours) and sends models only that text. Text, code, errors and UI come through well; photos and visual detail don't.", async x => drawPV(await API().SetPrivateVision(x))), pvs);
    pvs.textContent = pv.engine ? `On-device OCR: ${pv.engine} · ${pv.note}` : (pv.note || "No on-device OCR found");
  };
  if (API().PrivateVision) API().PrivateVision().then(drawPV).catch(() => {});
  g3.append(bx3, pvb, rows, tr);

  // 04 history
  const clr = h("button", "btn ghost xs", "Clear");
  const g4 = group("Recent", "", clr);
  const hist = h("div", "rows");
  const drawHist = list => {
    hist.innerHTML = "";
    (list || []).slice(0, 12).forEach(e => hist.append(h("div", "r hrow", `<span class="label weak ntime">${esc(fmtWhen(e.at))}</span><div class="main"><div class="t">${esc(e.title)}${e.kind === "suggest" ? ` <span class="pill acc">Suggestion</span>` : ""}${e.error ? ` <span class="pill">Not shown</span>` : ""}</div><div class="s">${esc(e.error || e.body || "")}</div></div><span class="label weak">${esc(e.method || "")}</span>`)));
    if (!(list || []).length) hist.append(h("div", "empty", "<b>Nothing yet</b>Notifications Blaze sends show up here."));
  };
  drawHist(n.history);
  clr.onclick = async () => { await API().ClearNotifyHistory(); drawHist([]); };
  g4.append(hist);
  col.append(g1, g2, g3, g4);
};

function fmtWhen(t) {
  const d = new Date(t); if (isNaN(d)) return "";
  const now = new Date();
  const hm = `${pad(d.getHours())}:${pad(d.getMinutes())}`;
  return d.toDateString() === now.toDateString() ? hm : `${d.toLocaleString("en", { month: "short" })} ${d.getDate()} ${hm}`;
}
