// First-run onboarding (v0.19.14): two steps, sign in and an optional folder, then a chat whose
// first answer works. Nothing else is asked up front: notifications, the shortcut, the tray and
// start-at-login live in Settings, and Blaze asks for a folder or notifications when a task
// first needs one (perms.js, permissions.go). Skippable, resumable (the step is saved in
// desktop.json) and re-openable from Settings › Behaviour › "Run setup again".

const Onboard = (() => {
  const STEPS = [["signin", "Sign in"], ["folder", "Folder"]]; // same list as onboardSteps in onboarding.go
  const FREE = ["gemini", "groq", "cerebras", "openrouter", "mistral", "nvidia"];
  const reduce = window.BlazeMotion.query;
  let step = 0, root = null, pick = "", more = false;

  const hasModel = st => (st.providers || []).some(p => p.ready && p.id !== "pollinations");
  const shouldOpen = st => !st.setup_done || (!hasModel(st) && !(st.prefs && st.prefs.onboard_skipped));

  // The particle orb; with reduced motion it is drawn once and kept as a still image.
  function orb(size, cls) {
    const cv = h("canvas", "oborb " + (cls || "")); const dpr = Math.min(2, window.devicePixelRatio || 1);
    cv.width = cv.height = Math.round(size * dpr); cv.style.width = cv.style.height = size + "px";
    particleOrb(cv, size);
    if (reduce.matches) setTimeout(() => { if (!cv.isConnected) return; const img = h("img", cv.className); img.src = cv.toDataURL(); img.style.cssText = cv.style.cssText; cv.replaceWith(img); }, 120);
    return cv;
  }

  async function open(at) {
    S = await API().State();
    if (at == null) { try { at = (await API().Onboarding()).step || 0; } catch (e) { at = 0; } }
    if (typeof loadAccount === "function") { try { await loadAccount(); } catch (e) {} }
    if (window.CloudModels) { try { await CloudModels.load(); } catch (e) {} }
    step = Math.max(0, Math.min(STEPS.length - 1, at | 0));
    if (!root) {
      root = h("div", ""); root.id = "onboard";
      root.setAttribute("role", "dialog"); root.setAttribute("aria-modal", "true"); root.setAttribute("aria-label", "Set up Blaze");
      document.body.append(root);
    }
    document.body.classList.add("onboarding");
    draw();
  }
  function close() { if (root) root.remove(); root = null; document.body.classList.remove("onboarding"); }
  async function go(n) { step = Math.max(0, Math.min(STEPS.length - 1, n)); API().SetOnboardStep(step); S = await API().State(); draw(); }
  async function skip() { await API().SkipOnboarding(); await refreshState(); close(); hero(); land(); }
  async function finish() { await API().FinishOnboarding(); await refreshState(); close(); hero(); land(); }

  // Setup lands in the chat with a first message ready that answers on a fresh install
  // (the web, or the folder just picked); nothing is sent until the user presses Enter.
  async function land() {
    if (window.Shell) await Shell.go("chat"); else show("work");
    const i = $("#input"); if (!i || i.value.trim()) return;
    const tries = window.Home && Home.starters ? Home.starters() : [];
    const first = tries.find(t => t[0] === "Explore") || tries[0];
    if (!first) return;
    i.value = first[1]; if (typeof grow === "function") grow(); if (typeof sendLabel === "function") sendLabel();
    i.focus(); i.select();
  }

  function draw() {
    if (!root) return;
    const [id] = STEPS[step];
    root.innerHTML = "";
    const card = h("div", "obpanel");
    const top = h("div", "obtop", `<img class="logo" src="logo.svg" alt=""><span class="bname">Blaze</span><span class="obdots" aria-label="Step ${step + 1} of ${STEPS.length}">${STEPS.map((x, i) => `<span class="${i === step ? "on" : i < step ? "done" : ""}"></span>`).join("")}</span><span class="label weak obcount">${step + 1} of ${STEPS.length} · ${esc(STEPS[step][1])}</span><span class="grow"></span>`);
    const sk = h("button", "btn ghost sm", "Skip setup"); sk.onclick = skip; top.append(sk);
    const body = h("div", "obbody " + id);
    const nav = h("div", "obnav");
    card.append(top, body, nav);
    root.append(card);
    ({ signin, folder })[id](body, nav);
  }

  function navBtns(nav, next, opts = {}) {
    if (step > 0) { const b = h("button", "btn line", `<svg class="i sm" viewBox="0 0 16 16"><path d="m10 4-4 4 4 4"/></svg>Back`); b.onclick = () => go(step - 1); nav.append(b); }
    nav.append(h("span", "grow"));
    if (opts.note) nav.append(h("span", "obnote", opts.note));
    const n = h("button", opts.quiet ? "btn line" : "btn accent", `${esc(next || "Continue")}${icon("chev", "i sm")}`);
    n.onclick = opts.onNext || (() => go(step + 1));
    nav.append(n);
  }

  // The orb above each step's title.
  const head = (body, title, lead, size) => { const o = h("div", "obhead"); o.append(orb(size || 72, "obmini")); o.append(h("div", "obht", `<h1>${title}</h1><p class="lead">${lead}</p>`)); body.append(o); };

  // 01 · Sign in (Blaze Cloud: no keys), or connect a model in Custom mode.
  function signin(body, nav) {
    if (!(window.CloudModels && CloudModels.on())) return model(body, nav);
    const ch = CloudModels.choice || {};
    const ok = ch.signed_in || (typeof ACCT !== "undefined" && ACCT && ACCT.signed_in);
    const email = ch.email || (typeof ACCT !== "undefined" && ACCT && ACCT.user && ACCT.user.email) || "";
    head(body, ok ? "You're signed in" : "Sign in to Blaze", ok ? "Blaze Cloud answers with no API keys and picks a good model for each message." : "Blaze Cloud answers with no API keys. It's the same account as Blaze on your phone.");
    if (ok) body.append(h("div", "obok", `<span class="sq2 ok">${icon("check", "i sm")}</span><span>Signed in as <b>${esc(email || "your Blaze account")}</b></span>`));
    else if (typeof signinForm === "function") {
      const f = signinForm(async () => { if (window.CloudModels) await CloudModels.load(); S = await API().State(); go(1); });
      f.classList.add("obsign"); body.append(f);
    }
    const own = h("button", "btn ghost xs obown", "Use my own API keys instead"); own.onclick = () => { close(); show("settings", "models"); };
    nav.append(own); // in the footer's free left slot (step 1 has no Back), so it never scrolls out of view at 800x600
    navBtns(nav, ok ? "Continue" : "Not now", { note: ok ? "" : "Blaze answers once you sign in.", quiet: !ok });
  }

  // 02 · Folder (optional): where Blaze reads and edits files.
  function folder(body, nav) {
    const f = (S.prefs && S.prefs.folder) || "", home = (S.info && S.info.home) || "";
    const chosen = !!f && f !== home;
    const shown = home && f.startsWith(home) ? "~" + f.slice(home.length) : f;
    head(body, "Pick a folder to work in", "Optional. Blaze reads and edits files only where you point it, and asks before anything risky. Change it any time from the folder chip.");
    const box = h("div", "obfolder" + (chosen ? " set" : ""));
    box.innerHTML = chosen ? `<span class="sq2 ok">${icon("check", "i sm")}</span><span class="mono obpath">${esc(shown)}</span><span class="grow"></span>` : `<span class="obpath sub">No folder yet. Blaze starts in your home folder.</span><span class="grow"></span>`;
    const pick = h("button", chosen ? "btn line sm" : "btn accent sm", chosen ? "Change" : "Choose a folder");
    pick.onclick = async () => { pick.disabled = true; const d = await API().PickFolder(); if (d) { S.prefs.folder = d; setFolder(d); } S = await API().State(); draw(); };
    box.append(pick); body.append(box);
    if (step > 0) { const b = h("button", "btn line", `<svg class="i sm" viewBox="0 0 16 16"><path d="m10 4-4 4 4 4"/></svg>Back`); b.onclick = () => go(step - 1); nav.append(b); }
    nav.append(h("span", "grow"));
    const st = h("button", "btn accent", `${chosen ? "Start chatting" : "Skip and start"}${icon("chev", "i sm")}`); st.onclick = () => finish();
    nav.append(st);
  }

  // 01 (Custom mode) · Pick & connect a model
  function model(body, nav) {
    const provs = (S.providers || []).filter(p => p.id !== "pollinations");
    const ready = provs.filter(p => p.ready);
    head(body, "Connect a model", "Blaze needs one AI model that can use tools. These have free tiers; add a key and Blaze tests it straight away. Add more later and Auto switches when one is busy.");
    const grid = h("div", "obtiles");
    const shown = provs.filter(p => more || FREE.includes(p.id) || p.ready);
    if (!pick) pick = (ready[0] || shown[0] || {}).id || "";
    shown.forEach(p => {
      const t = h("button", "obtile" + (p.id === pick ? " on" : "") + (p.ready ? " ready" : ""), `${logo(p.id, "big")}<span class="tn">${esc(p.short)}</span><span class="label ${p.ready ? "acc" : "weak"}">${p.ready ? "Connected" : p.keyless ? "Local" : p.free ? "Free tier" : "Paid"}</span>${p.ready ? `<span class="tick">${icon("check", "i sm")}</span>` : ""}`);
      t.onclick = () => { pick = p.id; draw(); };
      grid.append(t);
    });
    if (!more) { const m = h("button", "obtile moretile", `<span class="blogo line big"><svg class="i" viewBox="0 0 16 16">${IC.plus}</svg></span><span class="tn">More providers</span><span class="label weak">${provs.length - shown.length} more</span>`); m.onclick = () => { more = true; draw(); }; grid.append(m); }
    body.append(grid);
    const p = provs.find(x => x.id === pick);
    if (p) body.append(keyPanel(p));
    navBtns(nav, hasModel(S) ? "Continue" : "Skip for now", { note: hasModel(S) ? "" : "Blaze can't act until it has a model." });
  }

  function keyPanel(p) {
    const box = h("div", "obkey");
    const head = h("div", "obkh", `${logo(p.id)}<div><div class="tn">${esc(p.short)}</div><div class="sub small">${p.ready ? `Connected · using <span class="mono">${esc(p.model || "its default model")}</span>` : p.keyless ? "Runs on this computer. Install it, then Blaze checks it's running." : p.id === "custom" ? "Any OpenAI-compatible service. Set its address in Accounts › API Keys." : `Paste your ${esc(p.short)} key.${p.key_hint && p.key_hint !== "…" ? ` It starts <span class="mono">${esc(p.key_hint)}</span>.` : ""}`}</div></div>`);
    box.append(head);
    const out = h("div", "obout small");
    const result = r => { if (r && r.ok) { out.innerHTML = `<span class="ok">${icon("check", "i sm")} Works. Saved in ${esc(r.where || "your keychain")}; using <span class="mono">${esc(r.model || "")}</span>.</span>`; setTimeout(async () => { S = await API().State(); setModel(S.model, S.selection); draw(); }, 900); } else out.innerHTML = `<span class="bad">${esc((r && r.error) || "That didn't work.")}</span>`; };
    if (p.keyless) {
      const row = h("div", "keyrow");
      if (p.key_page) { const g = h("button", "btn line sm", `Get ${esc(p.short)}${icon("ext", "i sm")}`); g.onclick = () => API().OpenURL(p.key_page); row.append(g); }
      const c = h("button", "btn accent sm", p.ready ? "Check again" : "Check and add");
      c.onclick = async () => { c.disabled = true; c.textContent = "Checking…"; result(await API().AddProvider(p.id, "")); c.disabled = false; c.textContent = "Check and add"; };
      row.append(c); box.append(row);
    } else if (p.id === "custom") {
      const b = h("button", "btn line sm", `Open API Keys${icon("chev", "i sm")}`); b.onclick = () => { close(); show("accounts", "keys"); };
      box.append(b);
    } else {
      const row = h("div", "keyrow", `<input class="field mono" type="password" autocomplete="off" spellcheck="false" placeholder="${esc(p.ready ? "Add another key (optional)" : p.key_hint && p.key_hint !== "…" ? p.key_hint : "Paste your key")}">`);
      if (p.key_page) { const g = h("button", "btn line sm", `Get a key${icon("ext", "i sm")}`); g.onclick = () => API().OpenURL(p.key_page); row.append(g); }
      const sv = h("button", "btn accent sm", "Test & save");
      sv.onclick = async () => { const v = $("input", row).value.trim(); if (!v) { $("input", row).focus(); return; } sv.disabled = true; sv.textContent = "Testing…"; out.textContent = ""; result(await API().AddProvider(p.id, v)); sv.disabled = false; sv.textContent = "Test & save"; };
      $("input", row).onkeydown = e => { if (e.key === "Enter") sv.click(); };
      row.append(sv); box.append(row);
      if (p.id === "openrouter") { const oc = h("button", "btn ghost sm", "Sign in with OpenRouter instead"); oc.onclick = async () => { oc.disabled = true; oc.textContent = "Waiting for you in the browser…"; result(await API().ConnectOpenRouter()); oc.disabled = false; oc.textContent = "Sign in with OpenRouter instead"; }; box.append(oc); }
    }
    box.append(out);
    return box;
  }

  function refresh() { if (root) draw(); }

  document.addEventListener("keydown", e => {
    if (!root) return;
    if (e.key === "Enter" && !e.target.closest("input, textarea, button, form")) { const n = $(".obnav .btn:last-child", root); if (n) { e.preventDefault(); n.click(); } }
  });

  return { open, close, shouldOpen, refresh, redraw: () => draw(), go: n => go(n), steps: STEPS.map(x => x[0]), get step() { return step; } };
})();
window.Onboard = Onboard;

// show("setup") opens the flow over the chat.
window.VIEWS = window.VIEWS || {};
window.VIEWS.setup = (el, at) => { workView(el); Onboard.open(at); };

// Settings › Behaviour › Setup: run it again.
const _setBehaviourBase = setBehaviour;
setBehaviour = async function (col, redraw) {
  await _setBehaviourBase(col, redraw);
  const g = group("Setup", "The first-run walkthrough: sign in and pick a folder.");
  const b = h("button", "btn line sm", `${icon("retry", "i sm")}Run setup again`);
  b.onclick = () => show("setup", 0);
  g.append(b); col.append(g);
};

// Going anywhere else closes the flow (its step is saved, so show("setup") resumes).
const _showBase = show;
show = function (v, arg) {
  if (v !== "setup" && document.body.classList.contains("onboarding")) Onboard.close();
  return _showBase(v, arg);
};
