// v0.19 Home tab: Blaze Mobile's Home board on the desktop. A greeting, a quick ask, and the board:
// cards grouped into category stacks (weather, news, prices, sports, GitHub, schedule, deadlines, pick up
// where you left off). Content comes from Go (internal/home, home_app.go); cards fetch lazily when they
// are older than 50 minutes, at most 3 at a time. Links open in the system browser.
const Home = (() => {
  const FRESH = 50 * 60 * 1000, STALE = 3 * 60 * 60 * 1000;
  const H = { widgets: [], data: {}, stacks: [], open: new Set(), loading: new Set(), greeting: "", name: "", date: "", last: 0 };
  let el = null, queue = [], running = 0, timer = 0;

  const TI = {
    weather: '<circle cx="8" cy="8" r="2.75"/><path d="M8 1.5v1.5M8 13v1.5M1.5 8H3M13 8h1.5M3.4 3.4l1 1M11.6 11.6l1 1M3.4 12.6l1-1M11.6 4.4l1-1"/>',
    news: '<rect x="2" y="2.5" width="12" height="11"/><path d="M4.5 5.5h7M4.5 8h7M4.5 10.5h4"/>',
    sports: '<circle cx="8" cy="8" r="6"/><path d="m8 4.5 3 2.2-1.1 3.6H6.1L5 6.7z"/>',
    prices: '<path d="M2 12.5 6 8.5l3 2.5 5-6"/><path d="M10.5 5H14v3.5"/>',
    github: '<path d="M5 4.5 1.5 8 5 11.5M11 4.5 14.5 8 11 11.5"/>',
    schedule: '<circle cx="8" cy="8" r="6"/><path d="M8 4.5V8l2.5 1.5"/>',
    deadlines: '<path d="M3.5 14V2.5M3.5 3h8l-1.5 3 1.5 3h-8"/>',
    recents: '<path d="M13 3.5v3h-3"/><path d="M12.6 6.5A5 5 0 1 0 13 9"/>',
    into: '<path d="M8 1.5v4M8 10.5v4M1.5 8h4M10.5 8h4"/>',
    custom: '<path d="M3 13h3l7-7-3-3-7 7z"/>',
  };
  const tile = t => `<span class="htile"><svg class="i sm" viewBox="0 0 16 16">${TI[t] || TI.custom}</svg></span>`;
  const ago = (t, now) => {
    if (!t) return "Not updated yet";
    const m = Math.max(0, Math.floor((now - t) / 60000));
    return "Updated " + (m < 1 ? "just now" : m < 60 ? m + " min ago" : m < 1440 ? Math.floor(m / 60) + " h ago" : m < 2880 ? "yesterday" : Math.floor(m / 1440) + " days ago");
  };
  const lines = size => size === "S" ? 0 : size === "L" ? 6 : 3;
  const LOCAL = ["schedule", "deadlines", "recents", "custom", "into"];

  async function load() {
    const v = await API().HomeBoard();
    if (!v) return;
    Object.assign(H, { widgets: v.widgets || [], stacks: v.stacks || [], greeting: v.greeting || "", name: v.name || "", date: v.date || "", last: v.last_refresh || 0 });
    H.data = Object.assign({}, v.data || {});
  }

  // ---------- refresh (lazy, 3 at a time) ----------
  function due(id) {
    const d = H.data[id], w = H.widgets.find(x => x.id === id);
    if (!w) return false;
    if (LOCAL.includes(w.type)) return true; // read on this computer: cheap
    return !d || !d.fetched || Date.now() - d.fetched >= FRESH;
  }
  function refresh(ids) {
    ids.forEach(id => { if (!H.loading.has(id) && !queue.includes(id)) queue.push(id); });
    pump();
  }
  function pump() {
    while (running < 3 && queue.length) {
      const id = queue.shift(); running++; H.loading.add(id); paintCard(id);
      Promise.resolve(API().HomeRefresh(id, navigator.language || "en-US")).then(d => { if (d) H.data[id] = d; })
        .catch(() => {}).finally(() => { running--; H.loading.delete(id); H.last = Math.max(H.last, (H.data[id] || {}).fetched || 0); paintCard(id); stamp(); pump(); });
    }
  }

  // ---------- view ----------
  // v0.19.14: Home is one big chat input (folder chip, three starters, recent chats) with the orb
  // above it. The card board stays, below, once it has cards ("Add a Home card" otherwise).
  async function homeView(root) {
    el = h("div", "homepage"); root.append(el);
    const col = h("div", "hcol hhome"); el.append(col);
    const head = h("div", "hhero", `<div class="horb"></div><div class="ph"><div class="label hdate"></div><h1 class="hgreet"></h1></div>`);
    $(".horb", head).append(orbEl(56));
    col.append(head);
    col.append(askBox());
    col.append(h("div", "hpermslot"));
    col.append(startersEl());
    const recent = h("div", "hrecent"); col.append(recent);
    const cards = h("div", "hcards"); col.append(cards);
    cards.append(h("div", "hcardshead", `<span class="label">Your cards</span><span class="hstamp sub"></span><span class="grow"></span>
      <button class="btn ghost xs hrefresh" title="Refresh every card">${icon("retry", "i sm")}<span>Refresh</span></button><button class="btn line xs hadd" data-pop="1">${icon("plus", "i sm")}<span>Add a card</span></button>`));
    const board = h("div", "hboard"); cards.append(board);
    const dk = (S && S.desktop) || {};
    if (dk.hotkey_ok && dk.hotkey_label) col.append(h("div", "hhint label weak", `Press <span class="kbd">${esc(dk.hotkey_label)}</span> anywhere for quick ask`));
    $(".hrefresh", cards).onclick = () => refresh(H.widgets.filter(w => !w.hidden).map(w => w.id));
    $(".hadd", cards).onclick = e => { e.stopPropagation(); addPop(e.currentTarget); };
    const mine = el;
    setTimeout(() => { const i = $(".hinput", col); if (i && el === mine) i.focus(); }, 0);
    recents(recent);
    await load();
    if (el !== mine || !el.isConnected) return;
    $(".hdate", col).textContent = H.date;
    $(".hgreet", col).textContent = (H.greeting || hello()) + (H.name ? ", " + H.name : "") + ".";
    draw(); stamp();
    refresh(H.widgets.filter(w => !w.hidden && due(w.id)).map(w => w.id));
    clearInterval(timer);
    timer = setInterval(() => { if (view === "home" && el && el.isConnected) { refresh(H.widgets.filter(w => !w.hidden && due(w.id)).map(w => w.id)); stamp(); } }, 10 * 60 * 1000);
  }

  const hello = () => { const hr = new Date().getHours(); return hr >= 5 && hr < 12 ? "Good morning" : hr >= 12 && hr < 17 ? "Good afternoon" : "Good evening"; };

  // The particle orb (Blaze's mark); a still frame with reduced motion (particleOrb handles it).
  function orbEl(size) {
    const cv = h("canvas", "hmorb"), dpr = Math.min(2, window.devicePixelRatio || 1);
    cv.width = cv.height = Math.round(size * dpr); cv.style.width = cv.style.height = size + "px";
    if (typeof particleOrb === "function") particleOrb(cv, size);
    return cv;
  }

  // A real folder, not the home-folder default.
  const folderChosen = () => !!(S && S.prefs && S.prefs.folder && S.info && S.prefs.folder !== S.info.home);
  function folderLabel() {
    if (S && S.project) return S.project.name;
    return folderChosen() ? base(S.prefs.folder) : "Choose a folder";
  }

  // The big input: Enter sends, Shift+Enter is a new line. Sends to the one conversation.
  function askBox() {
    const f = h("form", "hask hbig", `<textarea class="hinput" rows="2" placeholder="Ask Blaze to do something…" aria-label="Ask Blaze"></textarea>
      <div class="hkbar"><button type="button" class="chip hfolder" title="The folder Blaze works in"><svg class="i sm" viewBox="0 0 16 16"><path d="M1.5 3.5h5l1.5 1.5h6.5v8h-13z"/></svg><span></span></button>
      <span class="grow"></span><span class="hkeys label weak">Enter to send</span><button class="btn accent sm hsend" type="submit" aria-label="Send">${icon("send", "i sm")}<span>Ask</span></button></div>`);
    const ta = $(".hinput", f), chip = $(".hfolder", f);
    const paintChip = () => { $("span", chip).textContent = folderLabel(); chip.classList.toggle("unset", !folderChosen() && !(S && S.project)); chip.title = folderChosen() ? S.prefs.folder : "Pick the folder Blaze works in (optional)"; };
    paintChip();
    chip.onclick = async () => { const d = await API().PickFolder(); if (d) { S.prefs.folder = d; setFolder(d); } paintChip(); };
    const fit = () => { ta.style.height = "auto"; ta.style.height = Math.min(220, ta.scrollHeight) + "px"; };
    ta.addEventListener("input", fit);
    ta.addEventListener("keydown", e => { if (e.key === "Enter" && !e.shiftKey && !e.isComposing) { e.preventDefault(); f.requestSubmit(); } });
    f.onsubmit = async e => {
      e.preventDefault(); const t = ta.value.trim(); if (!t || f.classList.contains("wait")) return;
      f.classList.add("wait");
      try { if (window.Perms) await Perms.gate("folder", t, $(".hpermslot", el)); } finally { f.classList.remove("wait"); }
      paintChip(); askBlaze(t, false, true);
    };
    return f;
  }

  // Three starters that answer on a fresh install: the web, the folder (when there is one), this PC.
  function starters() {
    return [["Weather", "What's the weather here this weekend?"],
      folderChosen() ? ["Explore", "What's in this folder? Give me a short tour."] : ["News", "What's new in AI this week? Five short bullets."],
      ["This PC", "How much disk space is left, and what's using the most?"]];
  }
  function startersEl() {
    const g = h("div", "hstarters");
    starters().forEach(([k, t]) => {
      const b = h("button", "hstarter", `<span class="label">${esc(k)}</span><span class="t">${esc(t)}</span>`);
      b.onclick = () => askBlaze(t, false, true);
      g.append(b);
    });
    return g;
  }

  // Recent chats: the one conversation and project chats, then chats from before one mind.
  async function recents(box) {
    let list = [], arch = [];
    try { list = await API().Conversations("") || []; } catch (e) {}
    try { arch = API().ArchivedConversations ? await API().ArchivedConversations() || [] : []; } catch (e) {}
    const rows = [...list.map(c => ({ t: c.id === "main" ? "Chat" : c.title || "Untitled", d: c.id === "main" ? "Your one conversation" : "", w: c.updated || "", go: () => c.id === "main" ? Shell.go("chat") : openChat(c.id) })),
      ...arch.map(c => ({ t: c.title || "Untitled", d: "Earlier chat", w: c.updated || "", go: () => show("archive", c.id) }))].slice(0, 5);
    if (!box.isConnected || !rows.length) return;
    box.append(h("div", "hrhead", `<span class="label">Recent</span><span class="grow"></span><button class="btn ghost xs hall">${icon("chev", "i sm")}<span>All chats</span><span class="kbd">Ctrl K</span></button>`));
    $(".hall", box).onclick = () => Shell.search();
    const ul = h("div", "hrecrows");
    rows.forEach(r => {
      const b = h("button", "hrecrow", `<span class="t">${esc(r.t)}</span>${r.d ? `<span class="d">${esc(r.d)}</span>` : ""}<span class="grow"></span><span class="w">${esc(r.w)}</span>`);
      b.onclick = r.go; ul.append(b);
    });
    box.append(ul);
  }

  function stamp() {
    const s = el && $(".hstamp", el); if (!s) return;
    const last = Math.max(H.last, ...Object.values(H.data).map(d => d.fetched || 0), 0);
    s.textContent = navigator.onLine === false ? "Offline · showing saved cards" : ago(last, Date.now());
  }

  function draw() {
    const board = el && $(".hboard", el); if (!board) return;
    board.innerHTML = "";
    const cards = el && $(".hcards", el);
    if (cards) cards.classList.toggle("none", !H.stacks.length);
    if (!H.stacks.length) {
      // No cards yet: keep the feature one click away, without an empty board on Home.
      const add = h("button", "btn ghost xs haddfirst", `${icon("plus", "i sm")}<span>Add a Home card</span><span class="weak">weather, prices, scores, news</span>`);
      add.dataset.pop = "1"; add.onclick = e => { e.stopPropagation(); addPop(e.currentTarget); };
      board.append(add);
      return;
    }
    H.stacks.forEach(s => board.append(stackEl(s)));
  }

  const toggler = s => s.widgets.length > 1 ? () => { H.open.has(s.key) ? H.open.delete(s.key) : H.open.add(s.key); redrawStack(s.key); } : null;

  function stackEl(s) {
    const open = H.open.has(s.key), main = s.widgets[0], n = s.widgets.length;
    const box = h("div", "hstack" + (n > 1 ? " stacked" : "") + (open ? " open" : "") + (main.size === "L" ? " wide" : ""));
    box.dataset.key = s.key;
    box.append(card(main, { count: n, open, onToggle: toggler(s) }));
    if (n > 1 && !open) box.append(h("div", "hpeek p1"), ...(n > 2 ? [h("div", "hpeek p2")] : []));
    if (n > 1 && open) {
      s.widgets.slice(1).forEach(w => box.append(card(w, {})));
      const less = h("button", "btn line xs hless", `${icon("up", "i sm")}<span>Show less</span>`);
      less.onclick = () => { H.open.delete(s.key); redrawStack(s.key); };
      box.append(less);
    }
    return box;
  }
  function redrawStack(key) {
    const old = el && $(`.hstack[data-key="${CSS.escape(key)}"]`, el), s = H.stacks.find(x => x.key === key);
    if (old && s) old.replaceWith(stackEl(s));
  }
  function paintCard(id) {
    const old = el && $(`.hcard[data-id="${CSS.escape(id)}"]`, el); if (!old) return;
    const w = H.widgets.find(x => x.id === id); if (!w) return;
    const s = H.stacks.find(x => x.widgets.some(y => y.id === id));
    const isMain = s && s.widgets[0].id === id;
    old.replaceWith(card(w, isMain ? { count: s.widgets.length, open: H.open.has(s.key), onToggle: toggler(s) } : {}));
  }

  function card(w, o) {
    const d = H.data[w.id], loading = H.loading.has(w.id), now = Date.now();
    const size = w.size || "M";
    const stale = d && d.fetched && (d.error || now - d.fetched > STALE);
    const c = h("div", "card hcard size-" + size + (o.onToggle ? " click" : ""));
    c.dataset.id = w.id; c.dataset.type = w.type;
    const tags = (o.count > 1 && !o.open ? `<span class="pill acc">+${o.count - 1}</span>` : "") + (stale ? `<span class="pill">Stale</span>` : "");
    c.innerHTML = `<div class="hch">${tile(w.type)}<span class="htitle">${esc(w.title)}</span>${tags}<button class="btn ghost icon hmenu" data-pop="1" title="Options for ${esc(w.title)}" aria-label="Options for ${esc(w.title)}"><svg class="i sm" viewBox="0 0 16 16"><path d="M3.5 8h.01M8 8h.01M12.5 8h.01"/></svg></button></div><div class="hbody"></div>`;
    const body = $(".hbody", c);
    if (!d || (!d.headline && !d.value && !(d.lines || []).length && !d.error && !d.change)) {
      body.append(h("div", "skel s1"), ...(size !== "S" ? [h("div", "skel s2")] : []));
      if (!loading && !d) body.append(h("div", "hfoot", "Not updated yet"));
    } else bodyOf(body, w, d, size);
    if (d && d.error) body.append(h("div", "herr", esc(d.error)));
    if (d && d.action) body.append(actions(w, d.action));
    if (d && d.fetched && size !== "S") body.append(h("div", "hfoot", loading ? "Updating…" : ago(d.fetched, now)));
    else if (loading && d) body.append(h("div", "hfoot", "Updating…"));
    $(".hmenu", c).onclick = e => { e.stopPropagation(); menu(e.currentTarget, w); };
    const inner = e => e.target.closest("button, a, input, form, .hline[data-u]");
    if (o.onToggle) c.onclick = e => { if (!inner(e)) o.onToggle(); };
    else if (d && d.link) { c.classList.add("click"); c.onclick = e => { if (!inner(e)) openLink(d.link); }; }
    return c;
  }

  function bodyOf(body, w, d, size) {
    if (d.value) {
      body.append(h("div", "hval" + (w.type === "weather" || w.type === "prices" ? " big" : ""), esc(d.value)));
      if (d.headline && d.headline !== w.title) body.append(h("div", "hsub", esc(d.headline)));
    } else if (d.headline && d.headline !== w.title && size !== "S") body.append(h("div", "hhl", esc(d.headline)));
    if (d.change) {
      const dir = /^▲/.test(d.change) ? " up" : /^▼/.test(d.change) ? " down" : "";
      body.append(h("div", "hchange" + dir, esc(d.change)));
    }
    const n = lines(size), ls = d.lines || [];
    if (!n || !ls.length) return;
    const hero = ls[0] && ls[0].i && size !== "S" ? ls[0] : null;
    if (hero) {
      const im = h("button", "himg", `<img alt="" loading="lazy" referrerpolicy="no-referrer" src="${esc(hero.i)}">`);
      im.title = hero.t; $("img", im).onerror = () => im.remove();
      if (hero.u) im.onclick = () => openLink(hero.u);
      body.append(im);
    }
    const list = h("div", "hlines");
    ls.slice(0, n).forEach(l => {
      const r = h("div", "hline" + (l.u ? " link" : ""), `<div class="hlt"><div class="t">${esc(l.t)}</div>${l.s ? `<div class="s">${esc(l.s)}</div>` : ""}</div>` +
        (l.i && l !== hero ? `<img class="hthumb" alt="" loading="lazy" referrerpolicy="no-referrer" src="${esc(l.i)}">` : "") +
        (l.n ? (l.n === "Next" || l.n === "New" ? `<span class="pill acc">${esc(l.n)}</span>` : `<span class="hnote">${esc(l.n)}</span>`) : ""));
      const th = $(".hthumb", r); if (th) th.onerror = () => th.remove();
      if (l.u) { r.dataset.u = l.u; r.tabIndex = 0; r.onclick = () => openLink(l.u); r.onkeydown = e => { if (e.key === "Enter") openLink(l.u); }; }
      list.append(r);
    });
    body.append(list);
    if (ls.length > n) body.append(h("div", "hmore label weak", `+${ls.length - n} more`));
  }

  function actions(w, a) {
    const row = h("div", "hactrow");
    const btn = (label, cls, fn) => { const b = h("button", "btn " + cls + " xs", esc(label)); b.onclick = e => { e.stopPropagation(); fn(); }; row.append(b); };
    if (a === "city") btn("Type a city", "line", () => inline(row, "e.g. Lusaka, Lima, Manila", "Show weather", async v => { await API().HomeSetCity(w.id, v); await reload([w.id]); }));
    else if (a === "into") btn("Tell Blaze what you're into", "line", () => inline(row, "e.g. Arsenal, bitcoin, AI news, weather in Lusaka", "Add cards", async v => { const n = await API().HomeInto(v); await reload(); refresh(H.widgets.filter(x => due(x.id)).map(x => x.id)); toast(n ? `Added ${n} card${n === 1 ? "" : "s"}.` : "Nothing added."); }));
    else if (a.startsWith("ask:")) btn("Ask Blaze", "line", () => askBlaze(a.slice(4)));
    return row;
  }
  function inline(row, ph, label, fn) {
    row.innerHTML = "";
    const f = h("form", "hinline", `<input class="field" placeholder="${esc(ph)}"><button class="btn accent xs" type="submit">${esc(label)}</button>`);
    f.onclick = e => e.stopPropagation();
    f.onsubmit = e => { e.preventDefault(); const v = $("input", f).value.trim(); if (v) fn(v); };
    row.append(f); $("input", f).focus();
  }

  async function reload(refreshIds) {
    await load(); draw(); stamp();
    if (refreshIds) refresh(refreshIds);
  }

  function menu(anchor, w) {
    const st = H.stacks.find(s => s.widgets.some(x => x.id === w.id));
    const ids = H.stacks.flatMap(s => s.widgets.map(x => x.id)), i = ids.indexOf(w.id);
    openPop(anchor, p => {
      p.classList.add("hpop");
      p.innerHTML = `<div class="phd"><span class="label">${esc(w.title)}</span></div><div class="pbody"></div>`;
      const body = $(".pbody", p);
      const row = (ic, name, fn, dis) => { const b = h("button", "opt", `<span class="on-mark">${ic}</span><span class="col2"><span class="ot">${esc(name)}</span></span>`); b.disabled = !!dis; b.onclick = e => { e.stopPropagation(); closePop(); fn(); }; body.append(b); };
      row(icon("retry", "i sm"), "Refresh", () => refresh([w.id]));
      row(icon("up", "i sm"), "Move up", async () => { await API().HomeMove(w.id, -1); reload(); }, i <= 0);
      row(icon("down", "i sm"), "Move down", async () => { await API().HomeMove(w.id, 1); reload(); }, i < 0 || i >= ids.length - 1);
      const sz = h("div", "hsizes", `<span class="label">Size</span>` + ["S", "M", "L"].map(s => `<button class="btn xs ${(w.size || "M") === s ? "accent" : "line"}" data-s="${s}">${{ S: "Small", M: "Medium", L: "Large" }[s]}</button>`).join(""));
      $$("button", sz).forEach(b => b.onclick = async e => { e.stopPropagation(); closePop(); await API().HomeResize(w.id, b.dataset.s); reload(); });
      body.append(sz);
      if (w.type === "weather") row(icon("globe", "i sm"), "Change city", () => { const c = $(`.hcard[data-id="${CSS.escape(w.id)}"] .hbody`, el); if (c) { const r = actions(w, "city"); c.append(r); $("button", r).click(); } });
      row(icon("sparks", "i sm"), "Ask Blaze to change it", () => askBlaze(`Change my Home card “${w.title}”: `, true));
      row(icon("trash", "i sm"), "Remove", async () => { await API().HomeRemove(w.id); if (st) H.open.delete(st.key); await reload(); toast(`Removed “${w.title}”.`); });
    }, { width: 260 });
  }

  function addPop(anchor) {
    openPop(anchor, p => {
      p.classList.add("hpop");
      p.innerHTML = `<div class="phd"><span class="label">Add a card</span></div><div class="pbody"><form class="hinline"><input class="field" placeholder="Say what you want to see"><button class="btn accent xs" type="submit">Add</button></form><div class="hex"></div></div>`;
      const ex = $(".hex", p);
      ["Weather in Lusaka", "Top headlines", "AI news", "Bitcoin price", "USD to ZMW", "Arsenal scores", "AAPL stock", "golang/go repo on GitHub", "My deadlines", "My schedule"].forEach(t => {
        const b = h("button", "chip", esc(t)); b.onclick = e => { e.stopPropagation(); $("input", p).value = t; $("form", p).requestSubmit(); }; ex.append(b);
      });
      $("form", p).onclick = e => e.stopPropagation();
      $("form", p).onsubmit = async e => {
        e.preventDefault(); const v = $("input", p).value.trim(); if (!v) return;
        closePop(); const w = await API().HomeAdd(v);
        await reload(w && w.id ? [w.id] : null);
        if (w && w.title) toast(`Added “${w.title}”.`);
      };
      setTimeout(() => $("input", p).focus(), 0);
    }, { width: 360 });
  }

  // blaze: links open inside the app; web links in the system browser.
  async function openLink(u) {
    if (!u) return;
    if (u.startsWith("blaze:chat:")) return Shell.go("chat");
    if (u.startsWith("blaze:project:")) {
      const id = u.slice(14); leaveTurn(); const p = await API().OpenProject(id); convId = "";
      if (p && p.folder) { S.prefs.folder = p.folder; setFolder(p.folder); }
      S.project = p && p.id ? p : null; hero(); return show("project", id);
    }
    if (u.startsWith("blaze:folder:")) { const f = u.slice(13); if (API().SetFolder) await API().SetFolder(f); S.prefs.folder = f; setFolder(f); return Shell.go("chat"); }
    if (/^https?:\/\//.test(u)) API().OpenURL(u);
  }

  // Questions from Home go to the one conversation, like mobile's "The answer is in Chat".
  async function askBlaze(text, draft, fromHome) {
    await Shell.go("chat");
    const i = $("#input"); if (!i) return;
    i.value = text; grow(); sendLabel();
    if (draft) { i.focus(); i.setSelectionRange(text.length, text.length); return; }
    if (fromHome && window.Perms) Perms.skipNext(); // the folder ask already happened on Home (set only right before the send, so it can't go stale)
    submit();
  }

  function toast(t) {
    const x = h("div", "toast htoast", esc(t)); document.body.append(x);
    setTimeout(() => x.remove(), 3200);
  }

  window.VIEWS = window.VIEWS || {};
  window.VIEWS.home = homeView;
  return { load, refresh, reload, starters, state: H };
})();
window.Home = Home;
