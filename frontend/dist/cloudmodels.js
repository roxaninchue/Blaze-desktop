// v0.19 Blaze Cloud + Custom models.
// Settings › Models: "Blaze Cloud (recommended)" or "Custom" (your own providers and keys,
// per-task models, an opt-in Blaze Cloud backup). The model menu shows the same choice first.
// Go side: cloudmodels_app.go + internal/cloudai.
const CloudModels = (() => {
  IC.cloud = IC.cloud || '<path d="M4.5 12.5h7a3 3 0 0 0 .4-5.97A4 4 0 0 0 4.2 7.1 2.75 2.75 0 0 0 4.5 12.5z"/>';
  IC.key = IC.key || '<circle cx="5.5" cy="10.5" r="3"/><path d="m7.6 8.4 6-6M11.5 4.5l1.5 1.5"/>';
  let CH = null;       // ModelsChoice()
  let ST = null;       // CloudStatus() (network)
  const TASKS = [["chat", "Chat & work", "Answers and actions. Must be able to call tools."], ["code", "Code", "When your message is about code."], ["vision", "Pictures", "When you attach an image."]];

  async function load() { try { CH = await API().ModelsChoice(); } catch (e) { CH = CH || { mode: "cloud", overrides: [] }; } return CH; }
  async function status(force) { if (!ST || force) { try { ST = await API().CloudStatus(); } catch (e) { ST = null; } } return ST; }
  const isCloud = () => !CH || CH.mode !== "custom";

  function cloudLine(c, st) {
    if (!c.signed_in) return { cls: "warn", text: "Sign in to your Blaze account and I'm ready to go." };
    if (st && !st.ready && st.line) return { cls: "warn", text: st.line };
    if (st && st.ready) return { cls: "ok", text: `Signed in as ${c.email}. Ready.` };
    return { cls: "", text: `Signed in as ${c.email}.` };
  }

  // ---------- Settings › Models ----------
  async function settings(col, redraw, prev) {
    const c = await load();
    col.append(h("p", "lead cmlead", "Choose what answers your messages. Blaze Cloud needs no keys: it picks a good model for each message. Custom uses your own providers and keys."));
    const g = group("How Blaze answers");
    const ch = h("div", "choice cmchoice");
    const cloudB = h("button", isCloud() ? "on" : "", `<span class="radio"></span><span><span class="ct">${icon("cloud", "i sm")} Blaze Cloud <span class="pill acc">Recommended</span></span><span class="cd" style="display:block">Your Blaze account, no API keys. The best model for each message, with quiet retries.</span></span>`);
    const custB = h("button", isCloud() ? "" : "on", `<span class="radio"></span><span><span class="ct">${icon("key", "i sm")} Custom</span><span class="cd" style="display:block">Your own providers and keys, set up one by one.</span></span>`);
    cloudB.dataset.mode = "cloud"; custB.dataset.mode = "custom";
    [cloudB, custB].forEach(b => b.onclick = async () => { if (b.classList.contains("on")) return; CH = await API().SetModelsMode(b.dataset.mode); await refreshState(); redraw(); });
    ch.append(cloudB, custB); g.append(ch);

    // Blaze Cloud status row.
    const row = h("div", "cmstatus");
    const paintRow = s => {
      const l = cloudLine(c, s);
      row.className = "cmstatus " + l.cls;
      row.innerHTML = `<span class="dot ${l.cls === "ok" ? "" : "off"}"></span><span class="t">${esc(l.text)}</span><span class="grow"></span>`;
      if (!c.signed_in) { const b = h("button", "btn accent sm", "Sign in"); b.onclick = () => show("signin"); row.append(b); }
      else if (isCloud()) { const b = h("button", "btn line sm", "Check"); b.onclick = async () => { b.disabled = true; paintRow(await status(true)); }; row.append(b); }
    };
    paintRow(null);
    if (c.signed_in && isCloud()) status(true).then(paintRow); // fresh on every visit: the waiting list can change
    g.append(row);
    col.append(g);

    if (isCloud()) {
      col.append(h("p", "sub cmnote", "Prefer your own keys? Choose Custom. Your providers and keys stay saved either way."));
      return;
    }

    // Custom: default (the existing router), per-task models, backup.
    const g2 = group("Your default", "", h("span", "mono small cmcur", esc(c.custom || "")));
    g2.append(h("p", "sub", "Set it in the model menu, or below in BlazeRouter and the classic editor (Auto routes across every provider you added)."));
    col.append(g2);

    const g3 = group("Per-task models", "Optional. Leave a task on Default to use your default for it.");
    const st = await refreshState();
    const ready = (st.providers || []).filter(p => p.ready && p.id !== "pollinations");
    TASKS.forEach(([task, name, desc]) => {
      const ov = (c.overrides || []).find(o => o.task === task) || { task };
      const r = h("div", "cmtask");
      r.dataset.task = task;
      r.innerHTML = `<div class="cmt"><span class="tt">${esc(name)}</span><span class="td">${esc(desc)}</span></div>`;
      const ps = h("select", "field");
      ps.innerHTML = `<option value="">Default</option>` + ready.map(p => `<option value="${p.id}" ${ov.provider === p.id ? "selected" : ""}>${esc(p.short)}</option>`).join("");
      const ms = h("select", "field mono");
      const msg = h("div", "cmmsg small");
      const fillModels = async () => {
        const pid = ps.value;
        if (!pid) { ms.innerHTML = `<option value="">—</option>`; ms.disabled = true; return; }
        ms.disabled = true; ms.innerHTML = `<option>Loading…</option>`;
        const l = await API().ModelOptions(pid, false);
        const list = ((l && l.models) || []).filter(m => task === "vision" ? m.vision !== false : m.tools !== "no");
        const cur = ov.provider === pid ? ov.model : "";
        ms.innerHTML = `<option value="">Choose a model</option>` + list.map(m => `<option value="${esc(m.id)}" ${m.id === cur ? "selected" : ""}>${esc(m.id)}</option>`).join("");
        ms.disabled = false;
        if (l && l.error) msg.textContent = l.error;
      };
      const save = async () => {
        msg.textContent = "";
        if (ps.value && !ms.value) return;
        const err = await API().SetTaskModel(task, ps.value, ms.value);
        if (err) { msg.textContent = err; msg.classList.add("bad"); return; }
        msg.classList.remove("bad");
        ov.provider = ps.value; ov.model = ms.value;
        msg.textContent = ps.value ? "Saved." : "Uses your default.";
      };
      ps.onchange = async () => { await fillModels(); if (!ps.value) save(); };
      ms.onchange = save;
      const pick = h("div", "cmpick"); pick.append(ps, ms);
      r.append(pick, msg);
      g3.append(r);
      fillModels();
    });
    if (!ready.length) g3.append(h("div", "pnote", "Add a provider first (Providers & keys below)."));
    col.append(g3);

    const g4 = group("If your models fail");
    const tg = h("label", "toggle cmfallback", `<input type="checkbox" ${c.fallback ? "checked" : ""}><span><span class="tt">Use Blaze Cloud as a backup</span><span class="td">Off: Blaze tells you your model failed and doesn't switch. On: Blaze Cloud answers instead and says so.${c.signed_in ? "" : " Needs your Blaze account."}</span></span>`);
    $("input", tg).onchange = async e => { CH = await API().SetCloudFallback(e.target.checked); };
    g4.append(tg);
    col.append(g4);

    col.append(h("h2", "brh", "Providers"));
    if (prev) await prev(col, redraw);
  }

  // ---------- the model menu ----------
  function decorate(p, close) {
    const body = $(".pbody", p); if (!body || $(".cmopt", body)) return;
    const cloud = isCloud();
    const l = cloudLine(CH || {}, ST);
    const b = h("button", "opt cmopt" + (cloud ? " sel" : ""), `<span class="on-mark">${cloud ? icon("check", "i sm") : ""}</span><span class="col2"><span class="ot">Blaze Cloud <span class="pill acc">Recommended</span></span><span class="od">${esc(CH && CH.signed_in ? (l.cls === "warn" ? l.text : "The best model for each message, no keys") : "Sign in to your Blaze account to use it")}</span></span>`);
    b.onclick = async () => {
      if (CH && !CH.signed_in) { close(); show("signin"); return; }
      CH = await API().SetModelsMode("cloud"); await refreshState(); close();
    };
    body.prepend(b, h("div", "grp cmgrp", "Custom · your own models"));
    if (cloud) $$(".opt.sel", body).forEach(o => { if (o !== b) { o.classList.remove("sel"); const m = $(".on-mark", o); if (m) m.innerHTML = ""; } });
    const mg = $(".phd .mg", p); if (mg) { mg.textContent = "Settings"; mg.onclick = () => { close(); show("settings", "models"); }; }
  }

  function install() {
    // Settings › Models: our choice on top; BlazeRouter's card (router.js) stays for Custom.
    window.SETTINGS_FNS = window.SETTINGS_FNS || {};
    const prev = window.SETTINGS_FNS.models || (typeof setModels === "function" ? setModels : null);
    window.SETTINGS_FNS.models = (col, redraw) => settings(col, redraw, prev);
    // The model menu: Blaze Cloud first, then the Custom options as before.
    if (typeof modelPopover === "function") {
      const orig = modelPopover;
      window.modelPopover = async anchor => {
        await load();
        const wasOpen = popEl && popEl.dataset.kind === "model";
        await orig(anchor);
        if (!wasOpen && popEl && popEl.dataset.kind === "model") decorate(popEl, closePop);
      };
    }
    // Onboarding: Blaze Cloud needs no key, so it doesn't reopen setup for one.
    if (window.Onboard && Onboard.shouldOpen) {
      const so = Onboard.shouldOpen;
      Onboard.shouldOpen = st => (st && st.model === "Blaze Cloud") ? !st.setup_done : so(st);
    }
    if (window.runtime && window.runtime.EventsOn) {
      on("account", () => { ST = null; load().then(() => refreshState()); });
      on("model", () => load());
    }
    load();
  }

  // Home's "no model yet" callout: Blaze Cloud only needs a sign-in.
  async function heroCallout(box, classic) {
    await load();
    if (!isCloud()) { classic(); return; }
    if (CH.signed_in) return;
    const c = h("div", "callout acc cmhero", `${icon("cloud")}<div><b>Sign in to start.</b> Blaze Cloud answers with your Blaze account. No API keys needed. <a class="go" style="color:var(--accent);cursor:pointer">Sign in</a></div>`);
    c.style.marginTop = "18px"; $("a", c).onclick = () => show("signin");
    box.append(c);
  }

  install();
  return { load, status, heroCallout, on: isCloud, get choice() { return CH; } };
})();
window.CloudModels = CloudModels;
