// v0.19 one infinite chat (Blaze Mobile's Chat): no chats sidebar. The Chat tab is the one main
// conversation. It opens on its last turns (OpenMainPage); older turns page in as you scroll up
// (MainHistory), then the chats from before one mind, newest first (read-only, ReadArchived), so nothing
// saved is lost. Search finds any message (SearchHistory) and jumps to it. Projects keep their own chats
// on the Projects tab. Only the UI side lives here: what the model sees is assembled in Go.
const OneChat = (() => {
  const TURNS = 12;
  let st = fresh();
  function fresh() { return { start: 0, total: 0, loading: false, mainDone: false, arch: null, archAt: 0, pool: [], ctxFrom: 0, token: 0 }; }

  const agentHandles = msgs => {
    const out = new Set();
    msgs.forEach(m => { if (m.role === "tool") { const r = /agent "([^"]+)"/.exec(m.content || ""); if (r) out.add(r[1]); } });
    return out;
  };
  const ctxSep = () => h("div", "ctxsep", `<span class="label">Fresh context</span><span class="d">Blaze looks older things up from memory</span>`);

  // Renders messages with the chat's own renderer (loadConv) into a detached column; returns its nodes.
  function render(msgs, agents, start, ctxFrom) {
    const real = $("#log .col", pane), log = $("#log", pane);
    const keep = log ? log.scrollTop : 0;
    const tmp = h("div", "col"); real.replaceWith(tmp);
    try { loadConv({ messages: msgs, agents: agents || [] }); } finally { tmp.replaceWith(real); if (log) log.scrollTop = keep; }
    const hr = $(".hero", tmp); if (hr && !msgs.length) hr.remove();
    // Tag each user message with its index in the conversation (search jumps to it).
    const users = []; msgs.forEach((m, i) => { if (m.role === "user") users.push(start + i); });
    const els = [...tmp.children].filter(e => e.matches(".msg.user, .agnote, .apnote"));
    if (els.length === users.length) els.forEach((e, k) => {
      e.dataset.mi = users[k];
      if (ctxFrom && users[k] === ctxFrom) e.before(ctxSep());
    });
    return [...tmp.children];
  }

  // The bar at the top of the log: loads older messages when it scrolls into view.
  let io = null;
  function topBar() {
    const col = $("#log .col", pane); if (!col) return;
    let bar = $(".olderbar", col);
    if (!bar) { bar = h("div", "olderbar"); col.prepend(bar); }
    else if (bar !== col.firstElementChild) col.prepend(bar);
    const end = st.mainDone && st.arch && st.archAt >= st.arch.length;
    if (end) {
      bar.innerHTML = `<span class="label weak">${st.arch.length ? "The beginning of your chats with Blaze" : "The beginning of your conversation"}</span>`;
      bar.classList.add("end");
    } else {
      bar.classList.remove("end");
      bar.innerHTML = `<button class="btn ghost xs olderbtn">${icon("up", "i sm")}<span>${st.loading ? "Loading…" : st.mainDone ? "Earlier chats" : "Earlier messages"}</span></button>`;
      $(".olderbtn", bar).onclick = () => older();
    }
    if (!io && typeof IntersectionObserver === "function") {
      io = new IntersectionObserver(es => es.forEach(e => { if (e.isIntersecting && !st.loading && !$(".hero", $("#log .col", pane))) older(); }), { root: $("#log", pane), rootMargin: "400px 0px 0px 0px" });
    }
    if (io) { io.disconnect(); if (!end) io.observe(bar); }
  }

  // Prepends nodes keeping what's on screen where it is.
  function prepend(nodes) {
    const log = $("#log", pane), col = $("#log .col", pane), bar = $(".olderbar", col);
    const hr = $(".hero", col); if (hr) hr.remove();
    const fromBottom = log.scrollHeight - log.scrollTop;
    const anchor = bar ? bar.nextSibling : col.firstChild;
    nodes.forEach(n => col.insertBefore(n, anchor));
    log.scrollTop = log.scrollHeight - fromBottom;
  }

  // One page older: the main conversation first, then one earlier chat at a time.
  async function older() {
    if (st.loading || convId !== "main") return false;
    const token = st.token;
    st.loading = true; topBar();
    try {
      if (!st.mainDone) {
        const pg = await API().MainHistory(st.start, TURNS);
        if (token !== st.token || !pg) return false;
        const msgs = pg.messages || [];
        const mine = agentHandles(msgs);
        const ag = st.pool.filter(a => mine.has(a.handle)); st.pool = st.pool.filter(a => !mine.has(a.handle));
        if (msgs.length) prepend(render(msgs, ag, pg.start || 0, st.ctxFrom));
        st.start = pg.start || 0;
        if (st.start <= 0) st.mainDone = true;
        return true;
      }
      if (st.arch === null) st.arch = (API().ArchivedConversations ? await API().ArchivedConversations() : []) || [];
      if (token !== st.token) return false;
      if (st.archAt < st.arch.length) {
        const it = st.arch[st.archAt++];
        const c = await API().ReadArchived(it.id) || {};
        if (token !== st.token) return false;
        const nodes = render(c.messages || [], [], 0, 0).map(n => { n.classList.add("archmsg"); delete n.dataset.mi; return n; });
        const sep = h("div", "archsep", `<span class="label">Earlier chat</span><button class="t" title="Open it on its own">${esc(c.title || it.title || "Untitled")}</button><span class="d">${esc(it.updated || "")}</span>`);
        $(".t", sep).onclick = () => show("archive", it.id);
        prepend([sep, ...nodes]);
        return true;
      }
      return false;
    } finally {
      st.loading = false;
      if (token === st.token) topBar();
    }
  }

  // Opens the one conversation on its last turns.
  async function openMain() {
    leaveTurn();
    st.token++; const token = st.token;
    const pg = await API().OpenMainPage(TURNS) || {};
    if (token !== st.token) return;
    convId = "main";
    if (pg.folder && !(S && S.project)) { S.prefs.folder = pg.folder; setFolder(pg.folder); }
    const msgs = pg.messages || [];
    const mine = agentHandles(msgs);
    const live = a => a.status === "running" || a.status === "queued";
    const ag = (pg.agents || []).filter(a => mine.has(a.handle) || live(a));
    st = { ...fresh(), token, start: pg.start || 0, total: pg.total || 0, mainDone: !pg.start, ctxFrom: pg.context_from || 0,
      pool: (pg.agents || []).filter(a => !ag.includes(a)) };
    const col = $("#log .col", pane);
    if (!msgs.length) hero();
    else {
      col.innerHTML = "";
      render(msgs, ag, st.start, st.ctxFrom).forEach(n => col.append(n));
      if (st.ctxFrom && st.ctxFrom >= st.total) col.append(ctxSep()); // cleared, nothing sent since
    }
    topBar();
    if (pg.running) {
      const hr = $(".hero", col); if (hr) hr.remove();
      current = blazeMsg(); col.append(current.el); setBusy(true); thinking(true, "Still working…");
    }
    show(tab === "design" ? "design" : "work");
    scrollDown(true);
  }

  // The Chat tab from inside a project: back to the one conversation.
  async function toMain() {
    leaveTurn();
    const p = await API().OpenProject("");
    if (S) S.project = null;
    if (p && p.folder) { S.prefs.folder = p.folder; setFolder(p.folder); }
    hero();
    await openMain();
  }

  // Search jumped to message index i of the main conversation.
  async function jumpTo(i) {
    if (convId !== "main" || (S && S.project)) await go("chat");
    else if (view !== "work") show("work");
    let guard = 0;
    while (st.start > i && !st.mainDone && guard++ < 200) { if (!await older()) break; }
    const els = $$("#log .col [data-mi]", pane).filter(e => +e.dataset.mi <= i);
    const el = els[els.length - 1];
    if (!el) { scrollDown(true); return; }
    el.scrollIntoView({ block: "center" });
    el.classList.remove("flash"); void el.offsetWidth; el.classList.add("flash");
  }
  const go = m => window.Shell ? Shell.go(m) : show("work");

  // Launch: Home on first run, then the tab the user last left (Home or Chat); the Chat tab is always
  // the one conversation, so a project the backend reopened is left.
  async function boot() {
    const last = window.Shell ? Shell.launch : localStorage.getItem("blaze-shell-mode");
    if (S && S.project) {
      const p = await API().OpenProject(""); S.project = null;
      if (p && p.folder) { S.prefs.folder = p.folder; setFolder(p.folder); }
      hero();
    }
    await openMain();
    if (last !== "chat") show("home");
  }

  // Called after every show(): a crumb back to the project above a project chat.
  function decorate() {
    const v = $("#view"); if (!v) return;
    $$(".pcrumb", v).forEach(e => e.remove());
    if (!(S && S.project) || (view !== "work" && view !== "design")) return;
    const c = h("div", "pcrumb", `<button class="btn ghost xs">${icon("chev", "i sm back")}<span>${esc(S.project.name)}</span></button><span class="label weak">Project chat</span>`);
    $("button", c).onclick = () => show("project", S.project.id);
    v.prepend(c);
  }

  // refreshSide() without a sidebar: keep the tabs in step.
  function sync() { if (window.Shell) Shell.sync(); }

  // The Projects tab's list.
  async function projectsView(el) {
    const { col, acts } = page(el, "Projects", "Your projects", "Each project keeps its own chats, instructions and folder. Chat is your one conversation; it remembers across everything.");
    const nb = h("button", "btn accent", `${icon("plus", "i sm")}New project`); nb.onclick = () => newProject(); acts.append(nb);
    const list = await API().Projects() || [];
    const g = group("Projects", "", h("span", "count", String(list.length)));
    const rows = h("div", "rows");
    list.forEach((p, i) => {
      const r = h("div", "r click", `<span class="n">${pad(i + 1)}</span><div class="main"><div class="t">${esc(p.name)}</div><div class="d mono">${esc(p.folder || "")}</div></div><span class="label weak">Open</span>`);
      r.onclick = async () => {
        leaveTurn(); const pr = await API().OpenProject(p.id); convId = "";
        if (pr && pr.folder) { S.prefs.folder = pr.folder; setFolder(pr.folder); }
        S.project = pr && pr.id ? pr : p; hero(); show("project", p.id);
      };
      rows.append(r);
    });
    if (!list.length) rows.append(h("div", "empty", "<b>No projects yet</b>A project is a folder Blaze works in, with its own chats and instructions."));
    g.append(rows); col.append(g);
  }

  // Search every message: the one conversation, then earlier chats.
  function historyView(el, q) {
    const { col } = page(el, "Search", "Search your conversation", "Every message in Chat and in your earlier chats. Blaze remembers all of it either way.");
    const inp = h("input", "field hsearch"); inp.placeholder = "Search messages…"; inp.value = q || "";
    const out = h("div", "rows hresults");
    col.append(inp, out);
    let t = 0;
    const run = async () => {
      const v = inp.value.trim(); out.innerHTML = "";
      if (!v) { out.append(h("div", "empty", "<b>Type to search</b>Words from anything you or Blaze said.")); return; }
      const hits = (API().SearchHistory ? await API().SearchHistory(v) : []) || [];
      if (inp.value.trim() !== v) return;
      out.innerHTML = "";
      if (!hits.length) { out.append(h("div", "empty", `<b>Nothing found</b>No message has “${esc(v)}”.`)); return; }
      hits.forEach(x => {
        const r = h("div", "r click", `<span class="n">${x.role === "user" ? "You" : "Blaze"}</span><div class="main"><div class="t">${esc(x.snippet)}</div><div class="d">${esc(x.conv === "main" ? "Chat" : "Earlier chat · " + (x.title || "") + (x.when ? " · " + x.when : ""))}</div></div><span class="label weak">${x.conv === "main" ? "Jump" : "Open"}</span>`);
        r.onclick = () => x.conv === "main" ? jumpTo(x.index) : show("archive", x.conv);
        out.append(r);
      });
    };
    inp.addEventListener("input", () => { clearTimeout(t); t = setTimeout(run, 180); });
    run(); setTimeout(() => inp.focus(), 0);
  }

  window.VIEWS = window.VIEWS || {};
  window.VIEWS.projects = projectsView;
  window.VIEWS.history = historyView;

  // openChat("main") (switchProject, notifications, older code paths) opens the paged conversation.
  const _openChat = openChat;
  openChat = id => (id === "main" ? openMain() : _openChat(id));

  return { boot, openMain, toMain, older, jumpTo, decorate, sync, get state() { return st; } };
})();
window.OneChat = OneChat;
