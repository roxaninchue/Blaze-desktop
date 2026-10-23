// The calm shell (v0.11.0, plan part A), v0.19: no sidebar. The top bar has the B logo, the tabs
// Home | Chat | Projects (Blaze Mobile's order), Search (Ctrl+K), the BlazeRouter dot and an avatar
// menu; an Exit chip whenever another view is open. Chat is one continuous conversation (onechat.js),
// Home is the board (home.js), Projects keep their own chats. The old Work/Design tabs, theme button and
// model chip stay in the DOM with their handlers; they live in /views, the avatar menu and the + menu.
const Shell = (() => {
  const TABS = ["home", "chat", "projects"];
  let mode = TABS.includes(localStorage.getItem("blaze-shell-mode")) ? localStorage.getItem("blaze-shell-mode") : "home";
  // The tab to open at launch, read before boot's first refreshSide() overwrites the saved one
  // (v0.19.14 fix: launch always landed on Chat). Home unless the user last left Chat open.
  const launch = mode === "chat" ? "chat" : "home";
  const AV = '<svg class="i" viewBox="0 0 16 16"><circle cx="8" cy="6" r="2.75"/><path d="M2.75 14c.6-2.6 2.7-4 5.25-4s4.65 1.4 5.25 4"/></svg>';
  const SEARCH = '<svg class="i sm" viewBox="0 0 16 16"><circle cx="7" cy="7" r="4.5"/><path d="m10.5 10.5 3.5 3.5"/></svg>';

  function build() {
    document.body.classList.add("shell");
    const top = $("#top");
    // Home | Chat | Projects, centred in the bar.
    const pill = h("div", "tabs shpill"); pill.id = "pill"; pill.setAttribute("role", "tablist");
    pill.innerHTML = `<button data-mode="home" role="tab">Home</button><button data-mode="chat" role="tab">Chat</button><button data-mode="projects" role="tab">Projects</button>`;
    $$("button", pill).forEach(b => b.onclick = () => go(b.dataset.mode));
    $("#tabs").after(pill);
    // DESIGN · Exit, shown while a view is open.
    const exit = h("div", "shexit"); exit.id = "exitChip";
    exit.innerHTML = `<span class="label ink" id="exitName"></span><button class="btn ghost xs" title="Back to the chat (Esc)">${icon("x", "i sm")}Exit</button>`;
    $("button", exit).onclick = () => view === "design" ? show("work") : go(mode);
    pill.after(exit);
    // Search, then the router button (already there), then the avatar.
    const sb = h("button", "chip shsearch", `${SEARCH}<span>Search</span><span class="kbd">Ctrl K</span>`); sb.id = "searchBtn";
    sb.title = "Search chats, projects, views and commands (Ctrl+K)";
    sb.onclick = e => { e.stopPropagation(); search(); };
    const rb = $("#routerBtn");
    (rb || $("#themeBtn")).before(sb);
    const av = h("button", "shavatar", AV); av.id = "avatar"; av.title = "Account and settings"; av.dataset.pop = "1";
    av.onclick = e => { e.stopPropagation(); avatarMenu(av); };
    top.append(av);
    // The B logo goes Home.
    const brand = $("#top .brand");
    if (brand) { brand.style.cursor = "pointer"; brand.addEventListener("click", () => go("home")); }
    addEventListener("keydown", e => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "k") { e.preventDefault(); search(); }
      if (e.key === "Escape" && !tabOf(view) && view !== "design" && !popEl && !menuOpen() && !document.body.classList.contains("orb")) go(mode);
    });
    paint();
  }

  // Which tab a view belongs to ("" = a page from /views, shown with the Exit chip).
  function tabOf(v) {
    if (v === "home") return "home";
    if (v === "projects" || v === "project" || v === "newproject") return "projects";
    if (v === "work" || v === "design") return S && S.project ? "projects" : "chat";
    return "";
  }

  // A tab was clicked (or the Exit chip, Esc, the logo).
  async function go(m) {
    if (m === "home") return show("home");
    if (m === "projects") return S && S.project ? show("project", S.project.id) : show("projects");
    // Chat is always the one main conversation: leave the open project first.
    if (S && S.project && window.OneChat) return OneChat.toMain();
    if (window.OneChat && convId !== "main") return OneChat.openMain();
    show("work");
  }

  function setMode(m) { return go(TABS.includes(m) ? m : "chat"); }

  function paint() {
    $$("#pill button").forEach(b => b.classList.toggle("on", b.dataset.mode === mode));
    TABS.forEach(t => document.body.classList.toggle("mode-" + t, t === mode));
  }

  // After every show(): the tab, the Exit chip and the pill's state.
  function sync() {
    const t = typeof view !== "undefined" ? tabOf(view) : "";
    if (t) { mode = t; if (t !== "projects") localStorage.setItem("blaze-shell-mode", t); }
    paint();
    const inView = !t || view === "design";
    const chip = $("#exitChip"); if (!chip) return;
    chip.style.display = inView ? "" : "none";
    $("#exitName").textContent = (view === "history" ? "Search" : window.BlazeViews ? BlazeViews.label(view) : view).toUpperCase();
    $("#pill").classList.toggle("dim", !t);
    chips();
    if (window.OneChat) OneChat.decorate();
  }

  // Projects rail: a status chip on the project that is working right now.
  function chips() {
    $$("#projects .chat.proj").forEach(b => {
      let c = $(".shchip", b);
      const working = b.classList.contains("on") && typeof busy !== "undefined" && busy;
      if (working && !c) { c = h("span", "shchip", "Working"); b.append(c); }
      if (!working && c) c.remove();
    });
  }

  // Ctrl+K: chats, projects, views and commands in the composer picker.
  async function search() {
    const list = await API().Conversations(S && S.project ? S.project.id : "") || [];
    const projects = await API().Projects() || [];
    const arch = API().ArchivedConversations ? await API().ArchivedConversations() || [] : [];
    const items = [
      { name: "Search the conversation", desc: "Every message in Chat and your earlier chats", run: () => show("history") },
      ...list.map(c => ({ name: c.id === "main" ? "Chat" : c.title, desc: (c.id === "main" ? "Your one conversation" : "Chat") + " · " + (c.updated || ""), run: () => c.id === "main" ? go("chat") : openChat(c.id) })),
      ...arch.map(c => ({ name: c.title || "Untitled", desc: "Earlier chat · " + (c.updated || ""), run: () => show("archive", c.id) })),
      ...[{ id: "", name: "General" }, ...projects].map(p => ({ name: p.name, desc: "Project" + (p.folder ? " · " + p.folder : ""), run: () => switchProject(p.id) })),
      ...(window.BlazeViews ? BlazeViews.items().map(v => ({ ...v, desc: "View · " + v.desc })) : []),
      ...COMMANDS.filter(c => c.run).map(c => ({ name: "/" + c.name, desc: c.desc, run: c.run })),
    ];
    const go = () => { openMenu("sub", items, "Search"); const i = $("#input"); if (i) i.focus(); };
    if (view !== "work" && view !== "design") { show("work"); setTimeout(go, 0); } else go();
  }

  async function switchProject(id) {
    leaveTurn(); const p = await API().OpenProject(id); convId = "";
    if (p && p.folder) { S.prefs.folder = p.folder; setFolder(p.folder); }
    hero(); await refreshSide();
    if (id) show("project", id); else go("chat");
  }

  // Account, Settings, BlazeRouter, Theme, Model, Updates, Sign out.
  async function avatarMenu(anchor) {
    if (popEl && popEl.dataset.kind === "avatar") { closePop(); return; }
    let acct = null;
    try { acct = typeof loadAccount === "function" ? await loadAccount() : null; } catch (e) {}
    const dark = document.documentElement.dataset.theme === "dark";
    openPop(anchor, p => {
      p.dataset.kind = "avatar"; p.classList.add("shmenu");
      const u = acct && acct.signed_in && acct.user;
      const who = u ? esc(u.email || "Signed in") : "Not signed in";
      p.innerHTML = `<div class="phd"><span class="label">Account</span><span class="grow"></span><span class="shwho">${who}</span></div><div class="pbody"></div>`;
      const body = $(".pbody", p);
      const row = (ic, name, desc, fn, extra) => {
        const b = h("button", "opt", `<span class="on-mark">${ic}</span><span class="col2"><span class="ot">${esc(name)}</span>${desc ? `<span class="od">${esc(desc)}</span>` : ""}</span>${extra || ""}`);
        b.onclick = e => { e.stopPropagation(); fn(b); };
        body.append(b); return b;
      };
      if (u) {
        // Who you are, your plan, and your picture (blaze-auth 0.9.3), like the website's menu.
        const plan = typeof PLAN_NAMES !== "undefined" && PLAN_NAMES[PLAN] ? PLAN : "";
        const hd = h("div", "avhead");
        if (typeof avatar === "function") hd.append(avatar(u, true));
        hd.append(h("div", "avwho", `<div class="avname">${esc(u.name || u.email.split("@")[0])}</div><div class="sub small mono">${esc(u.email)}</div>${plan ? `<span class="avplan inline" data-plan="${plan}">${PLAN_NAMES[plan]}</span>` : ""}`));
        body.append(hd);
        if (plan !== "pro") row(icon("route", "i sm"), plan ? "Upgrade" : "Plans", plan === "standard" ? "Go Pro" : "Standard and Pro plans", () => { closePop(); API().OpenURL("https://blazeinchue.pages.dev/#pro"); });
        if (typeof pickAvatar === "function") row(AV, u.avatar_url ? "Change picture" : "Add picture", "PNG, JPEG or WebP", () => { closePop(); pickAvatar(); });
        if (u.avatar_url) row(icon("x", "i sm"), "Remove picture", "Back to your initial", async () => { closePop(); const r = await API().AccountAvatar(""); if (!r || r.error) { toast((r && r.error) || "Couldn't remove the picture.", "bad"); return; } await loadAccount(); });
      }
      row(AV, "Account", acct && acct.signed_in ? "Your Blaze account" : "Sign in to sync keys", () => { closePop(); show(acct && acct.signed_in ? "settings" : "signin", acct && acct.signed_in ? "account" : undefined); });
      row(icon("sliders", "i sm"), "Settings", "Behaviour, appearance, memory, skills", () => { closePop(); show("settings"); });
      if (window.Router && Router.view && Router.view.enabled) row(icon("layers", "i sm"), "BlazeRouter dashboard", (window.Router && Router.view && Router.view.note) || "Models, keys and usage", () => { closePop(); show("router"); });
      row(icon("route", "i sm"), "Model", ($("#routeText") || {}).textContent || "", () => { closePop(); modelPopover(anchor); });
      row(icon(dark ? "moon" : "sun", "i sm"), "Theme", dark ? "Dark · click for light" : "Light · click for dark", () => { const t = $("#themeBtn"); if (t) t.click(); closePop(); });
      row(icon("grid", "i sm"), "All views", "Memory, Jobs, Schedule, Accounts, Doctor…", () => { closePop(); BlazeViews.picker(); });
      if (window.BlazeMusic) row(BlazeMusic.mi("note", "i sm"), "Music", "Your songs, playlists, the Store", () => { closePop(); show("music"); });
      if (window.VIEWS && VIEWS.video) row(window.BlazeMusic ? BlazeMusic.mi("film", "i sm") : icon("open", "i sm"), "Video", "Your videos, series, subtitles", () => { closePop(); show("video"); });
      row(icon("dl", "i sm"), "Check for updates", UPD && UPD.available ? "A new version is ready" : "", () => { closePop(); show("settings", "updates"); });
      if (acct && acct.signed_in) row(icon("x", "i sm"), "Sign out", "", async () => { closePop(); await API().AccountSignOut(); if (typeof loadAccount === "function") await loadAccount(); });
    }, { width: 300 });
  }

  return { build, sync, setMode, go, search, tabOf, get mode() { return mode; }, get launch() { return launch; } };
})();
window.Shell = Shell;
// Two icons the shell needs that app.js didn't have.
IC.route = '<circle cx="3.5" cy="8" r="1.5"/><circle cx="12.5" cy="4" r="1.5"/><circle cx="12.5" cy="12" r="1.5"/><path d="M5 8h3l3-3.5M8 8l3 3.5"/>';
IC.grid = '<rect x="2.5" y="2.5" width="4.5" height="4.5"/><rect x="9" y="2.5" width="4.5" height="4.5"/><rect x="2.5" y="9" width="4.5" height="4.5"/><rect x="9" y="9" width="4.5" height="4.5"/>';
Shell.build();
