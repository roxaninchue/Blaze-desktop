// Connectors: the user's own accounts and MCP servers (detail pages, chat cards,
// toasts, /connect). The tabbed Accounts screen is accounts.js. Loaded after app.js.
// Sign-in happens in the system browser; the sign-in comes back to Blaze on
// 127.0.0.1 (or blaze://), and tokens live in the OS keychain.

IC.plug = '<path d="M5.5 2v3.5M10.5 2v3.5M3.5 5.5h9V8a4.5 4.5 0 0 1-9 0zM8 12.5v2"/>';
IC.link = '<path d="M7 9a2.5 2.5 0 0 0 3.5 0l2.5-2.5A2.5 2.5 0 0 0 9.5 3L8.8 3.7M9 7a2.5 2.5 0 0 0-3.5 0L3 9.5A2.5 2.5 0 0 0 6.5 13l.7-.7"/>';
const cicon = (id, big) => logo(id, big ? "big" : "");

let CV = null; // last Connectors() view

const STATUS = {
  connected: ["Connected", ""], expired: ["Expired", "ink"], needs_setup: ["Needs setup", "warn"],
  connecting: ["Connecting…", "warn"], not_connected: ["Not connected", "off"],
};
function statusLine(c) {
  const [t, cls] = STATUS[c.status] || STATUS.not_connected;
  let text = t;
  if (c.status === "connected") text = c.account ? `Connected as ${c.account}` : "Connected";
  if (c.status === "connected" && !c.enabled) text = "Off for Blaze" + (c.account ? ` · ${c.account}` : "");
  return `<span class="cstat" title="${esc(text)}"><span class="dot ${c.status === "connected" && !c.enabled ? "off" : cls}"></span><span>${esc(text)}</span></span>`;
}

async function loadConnectors() { CV = await API().Connectors() || { connectors: [], mcp: [] }; return CV; }

function refreshConnCount(n) {
  const el = $("#connCount"); if (!el) return;
  if (n == null) n = (CV ? (CV.connectors || []).filter(c => c.status === "connected" && c.enabled).length + (CV.mcp || []).filter(s => s.status === "ready").length : (S && S.connected) || 0);
  el.textContent = n ? pad(n) : "";
}

// The main action for a connector, as a button.
function connAction(c, after) {
  const b = h("button", "btn sm");
  const go = async () => {
    b.disabled = true; const r = await API().ConnectorConnect(c.id); b.disabled = false;
    if (r && r.status === "error") toast(r.error, "error");
    if (r && r.user_code && view !== "connector") { show("connector", c.id); return; }
    after && after(r);
  };
  if (c.status === "connected") { b.className = "btn line sm"; b.textContent = "Manage"; b.onclick = () => show("connector", c.id); }
  else if (c.status === "expired") { b.className = "btn accent sm"; b.innerHTML = `${icon("retry", "i sm")}Reconnect`; b.onclick = go; }
  else if (c.status === "connecting") { b.className = "btn ghost sm"; b.textContent = "Cancel"; b.onclick = async () => { await API().ConnectorCancel(c.id); after && after(); }; }
  else if (c.one_click) { b.className = "btn accent sm"; b.textContent = "Connect"; b.title = "Sign in to " + c.name + " in your browser. No keys needed."; b.onclick = go; }
  else if (c.status === "needs_setup" || c.auth === "token") { b.className = "btn line sm"; b.textContent = "Set up"; b.onclick = () => show("connector", c.id); }
  else { b.className = "btn accent sm"; b.textContent = "Connect"; b.onclick = go; }
  return b;
}

function mcpRows(list) {
  const rows = h("div", "rows");
  list.forEach((s, i) => {
    const st = { ready: ["", `${(s.tools || []).length} tools`], error: ["ink", "Can't connect"], unchecked: ["warn", "Not checked yet"], disabled: ["off", "Off for Blaze"] }[s.status] || ["off", ""];
    const where = s.transport === "stdio" ? [s.command].concat(s.args || []).join(" ") : s.url;
    const r = h("div", "r click", `${cicon(s.transport === "stdio" ? "local" : "mcp")}<div class="main"><div class="t">${esc(s.name)} <span class="pill">${esc(s.transport === "stdio" ? "local" : s.transport)}</span></div><div class="s mono small">${esc(where)}</div></div><span class="cstat"><span class="dot ${st[0]}"></span><span>${esc(st[1])}</span></span>`);
    r.onclick = () => show("mcp", s.id);
    rows.append(r);
  });
  if (!list.length) rows.append(h("div", "empty", "<b>No MCP servers yet</b>Add a remote server by URL, or a local one by its command."));
  return rows;
}

function addMcpForm(onDone) {
  const box = h("div", "box pad formgrid mcpform");
  let tr = "http";
  box.innerHTML = `<div class="seg trs"><button data-t="http" class="on">Remote · Streamable HTTP</button><button data-t="sse">Remote · SSE</button><button data-t="stdio">Local command</button></div>
    <div class="fg"><label class="label">Name</label><input class="field nm" placeholder="e.g. Linear, Files"></div>
    <div class="fg remote"><label class="label">Server URL</label><input class="field mono url" placeholder="https://example.com/mcp"></div>
    <div class="fg2 remote"><div class="fg"><label class="label">Token <span class="weak">optional</span></label><input class="field mono tok" type="password" placeholder="Saved in your system keychain"></div>
      <div class="fg"><label class="label">Header <span class="weak">optional</span></label><input class="field mono hdr" placeholder="Authorization"></div></div>
    <div class="fg local" style="display:none"><label class="label">Command</label><input class="field mono cmd" placeholder="npx"></div>
    <div class="fg local" style="display:none"><label class="label">Arguments</label><input class="field mono args" placeholder="-y @modelcontextprotocol/server-filesystem ~/Documents"></div>
    <div class="fg local" style="display:none"><label class="label">Environment <span class="weak">optional · KEY=value per line, \${VAR} reads yours</span></label><textarea class="field mono env" rows="2" placeholder="GITHUB_TOKEN=\${GITHUB_TOKEN}"></textarea></div>
    <div class="fg remote"><label class="label">More headers <span class="weak">optional · Name: value per line</span></label><textarea class="field mono hdrs" rows="2" placeholder="X-Workspace: blaze"></textarea></div>
    <div class="label weak note">Blaze lists the server's tools now. Each tool asks before it runs until you set it to Always allow. Servers are shared with Blaze CLI in ~/.blaze/mcp.json; a token stays in your system keychain.</div>
    <div class="keyrow"><button class="btn accent sm add">${icon("plus", "i sm")}Add server</button><button class="btn ghost sm cancel">Cancel</button><span class="out small"></span></div>`;
  $$(".trs button", box).forEach(b => b.onclick = () => {
    tr = b.dataset.t; $$(".trs button", box).forEach(x => x.classList.toggle("on", x === b));
    $$(".remote", box).forEach(x => x.style.display = tr === "stdio" ? "none" : "");
    $$(".local", box).forEach(x => x.style.display = tr === "stdio" ? "" : "none");
  });
  $(".cancel", box).onclick = () => onDone(false);
  $(".add", box).onclick = async () => {
    const out = $(".out", box), add = $(".add", box);
    add.disabled = true; out.className = "out small sub"; out.textContent = "Connecting and listing tools…";
    const r = await API().MCPAdd({ name: $(".nm", box).value, transport: tr, url: $(".url", box).value, token: $(".tok", box).value, header: $(".hdr", box).value, command: $(".cmd", box).value, args: $(".args", box).value, env: $(".env", box).value, headers: $(".hdrs", box).value });
    add.disabled = false;
    if (r && r.error && !(r.server && r.server.id)) { out.className = "out small bad"; out.textContent = r.error; return; }
    onDone(true, r && r.server);
  };
  return box;
}

let connRedraw = null;

function modeSeg(t, owner, after) {
  const seg = h("div", "seg modes");
  [["ask", "Ask"], ["always", "Always allow"], ["off", "Off"]].forEach(([k, n]) => {
    const b = h("button", t.mode === k ? "on" : "", n);
    b.title = k === "ask" ? "Show an approval card each time" : k === "always" ? "Run without asking" : "Hide this tool from Blaze";
    b.onclick = async () => { const err = await API().ConnectorToolMode(owner, t.name, k === t.default ? "" : k); if (err) toast(err, "error"); else { t.mode = k; $$("button", seg).forEach(x => x.classList.toggle("on", x === b)); after && after(); } };
    seg.append(b);
  });
  return seg;
}

const RISK = { read: "Reads", action: "Acts", write: "Sends · changes" };

function toolRows(tools, owner, c) {
  const rows = h("div", "rows trows");
  tools.forEach(t => {
    const r = h("div", "r", `<div class="main"><div class="t"><span class="mono small">${esc(t.name)}</span><span class="pill ${t.risk === "write" ? "ink" : ""}">${esc(RISK[t.risk] || t.risk)}</span></div><div class="s">${esc(t.desc)}</div></div>`);
    const a = h("div", "acts");
    if (t.needs_grant && c) { const g = h("button", "btn line xs", "Grant access"); g.title = "Ask Google for the permission this tool needs"; g.onclick = async () => { const res = await API().ConnectorGrant(c.id, t.name); if (res && res.status === "error") toast(res.error, "error"); }; a.append(g); }
    a.append(modeSeg(t, owner));
    r.append(a); rows.append(r);
  });
  return rows;
}

async function connectorView(el, id) {
  await loadConnectors();
  const c = (CV.connectors || []).find(x => x.id === id);
  if (!c) { show("accounts"); return; }
  const pg = h("div", "page"); const col = h("div", "col"); pg.append(col); el.append(pg);
  const back = h("button", "btn ghost xs back", `<svg class="i sm" viewBox="0 0 16 16"><path d="m10 4-4 4 4 4"/></svg>Accounts`); back.onclick = () => show(acctReturn());
  col.append(back);
  const head = h("div", "phead chead", `${cicon(c.id, true)}<div class="ph"><div class="label acc">Connector</div><h1>${esc(c.name)}</h1><div class="lead">${esc(c.blurb)} · ${statusLine(c)}</div></div>`);
  const acts = h("div", "acts"); head.append(acts); col.append(head);
  const redraw = () => { if (view === "connector") show("connector", id); };
  connRedraw = redraw;

  if (c.status === "connected" || c.status === "expired") {
    if (c.status === "expired") acts.append(connAction(c, redraw));
    const dc = h("button", "btn ghost sm", "Disconnect");
    dc.onclick = async () => { if (!dc.dataset.sure) { dc.dataset.sure = 1; dc.textContent = "Disconnect? Click again"; return; } await API().ConnectorDisconnect(id); toast(`${c.name} is disconnected`); redraw(); };
    acts.append(dc);
  } else if (c.status === "connecting") {
    acts.append(connAction(c, redraw));
  } else if (c.one_click) {
    // The one-click box below carries the Connect button.
  } else if (c.auth !== "token") {
    const cn = connAction(c, redraw); if (c.status === "needs_setup") { cn.className = "btn accent sm"; cn.textContent = "Connect"; cn.disabled = true; cn.title = "Fill in the setup first"; } acts.append(cn);
  }

  // One click: Blaze's sign-in service holds the developer app, so there's nothing to set up.
  const live = c.status === "connected" || c.status === "expired";
  if (c.one_click && !live && c.status !== "connecting") {
    const g = group("Connect");
    const bx = h("div", "box pad oneclick", `<div><div class="ocbadge"><span class="dot"></span>One click</div><div class="t" style="margin-top:8px">Sign in to ${esc(c.name)} in your browser</div><div class="s">Approve access and you're done: no developer app, no keys. Tokens stay in your system keychain.</div></div>`);
    const b = connAction(c, redraw); b.classList.remove("sm"); bx.append(b);
    g.append(bx); col.append(g);
  } else if (c.cloud === "soon" && !live && c.status !== "connecting") {
    col.append(h("div", "callout soonnote", `${icon("plug")}<div>One-click Connect for ${esc(c.name)} is coming soon. Until then, connect it with your own ${c.auth === "token" ? "token" : "developer app"} below.</div></div>`));
  }

  if (c.status === "expired") col.append(h("div", "callout", `${icon("retry")}<div>Your ${esc(c.name)} connection has expired. Reconnect it before Blaze can use it.</div>`));

  // Waiting for the browser or a device code.
  if (c.pending) {
    const g = group(c.pending.kind === "device" ? "Enter this code on GitHub" : "Finish in your browser");
    const bx = h("div", "box pad pend");
    if (c.pending.kind === "device") {
      bx.innerHTML = `<div class="devcode mono">${esc(c.pending.user_code)}</div><div class="sub small">Blaze opened <span class="mono">${esc(c.pending.url)}</span>. Type the code there and approve; this page updates by itself.</div>
        <div class="keyrow" style="margin-top:12px"><button class="btn line sm cp">${icon("copy", "i sm")}Copy code</button><button class="btn ghost sm op">${icon("ext", "i sm")}Open GitHub again</button></div>`;
      $(".cp", bx).onclick = () => { navigator.clipboard.writeText(c.pending.user_code); toast("Code copied"); };
    } else {
      bx.innerHTML = `<div class="waitline"><span class="sq"></span><span>Waiting for you to sign in to ${esc(c.name)} in your browser…</span></div>
        <div class="keyrow" style="margin-top:12px"><button class="btn line sm op">${icon("ext", "i sm")}Open the sign-in page again</button></div>${c.pending.one_click ? "" : `
        <details class="paste"><summary class="small sub">Browser showed an error page instead of coming back?</summary><div class="small sub" style="margin:8px 0">Copy the full address from the browser's address bar and paste it here.</div><div class="keyrow"><input class="field mono pu" placeholder="http://127.0.0.1:43117/callback?code=…"><button class="btn line sm pb">Finish</button></div></details>`}`;
      if ($(".pb", bx)) $(".pb", bx).onclick = async () => { const err = await API().ConnectorPasteCallback($(".pu", bx).value); if (err) toast(err, "error"); redraw(); };
    }
    $(".op", bx).onclick = () => API().OpenURL(c.pending.url);
    g.append(bx); col.append(g);
  }

  // Setup: fields, steps, console link, redirect choice.
  let setupG = null;
  if (c.status !== "connecting") {
    const g = group(c.auth === "token" ? "Connect with a token" : "Your developer app", c.auth === "token" ? "" : "Make a free developer app and paste its ID. Blaze then talks to " + esc(c.name) + " directly from this computer.");
    const steps = h("ol", "csteps"); (c.steps || []).forEach(s => steps.append(h("li", "", esc(s))));
    const consoleBtn = h("button", "btn line sm", `${icon("ext", "i sm")}Open ${esc(c.console_name || "the developer console")}`); consoleBtn.onclick = () => API().OpenURL(c.console);
    const fields = h("div", "formgrid cfields");
    (c.fields || []).forEach(f => {
      const fg = h("div", "fg", `<label class="label">${esc(f.label)}${f.set ? ' <span class="acc2">· saved</span>' : ""}</label>`);
      const row = h("div", "keyrow");
      const inp = h("input", "field mono"); inp.type = f.secret ? "password" : "text"; inp.placeholder = f.set && f.secret ? "•••••••• saved in your keychain · paste to replace" : f.hint; inp.value = f.value || "";
      const sv = h("button", "btn line sm", f.key === "token" ? "Connect" : "Save");
      if (f.key === "token") sv.className = "btn accent sm";
      sv.onclick = async () => {
        sv.disabled = true; const t = sv.textContent; sv.textContent = f.key === "token" ? "Checking…" : "Saving…";
        const err = await API().ConnectorSetField(c.id, f.key, inp.value);
        sv.disabled = false; sv.textContent = t;
        if (err) { toast(err, "error"); return; }
        if (f.key !== "token") toast(`${f.label} saved`);
        redraw();
      };
      inp.onkeydown = e => { if (e.key === "Enter") sv.click(); };
      row.append(inp, sv); fg.append(row); fields.append(fg);
    });
    const bx = h("div", "box pad csetup");
    bx.append(steps, h("div", "keyrow", ""), fields);
    bx.children[1].append(consoleBtn);
    if (c.auth === "oauth") {
      const rd = h("div", "fg");
      rd.innerHTML = `<label class="label">Redirect URI</label>`;
      const r2 = h("div", "keyrow");
      if (c.custom_scheme) {
        const seg = h("div", "seg");
        [["", "Loopback · 127.0.0.1"], ["scheme", "blaze:// link"]].forEach(([k, n]) => { const b = h("button", (c.redirect || "") === k ? "on" : "", n); b.onclick = async () => { await API().ConnectorSetRedirect(c.id, k); redraw(); }; seg.append(b); });
        r2.append(seg);
      }
      const uri = h("span", "mono small uri", esc(c.redirect_uri));
      const cp = h("button", "btn ghost xs", `${icon("copy", "i sm")}Copy`); cp.onclick = () => { navigator.clipboard.writeText(c.redirect_uri); toast("Redirect URI copied"); };
      r2.append(uri, cp); rd.append(r2);
      if (c.id === "google") rd.append(h("div", "note2", "Desktop app clients accept any 127.0.0.1 port, so there's nothing to add in Google Cloud."));
      fields.append(rd);
    }
    if (c.auth !== "token" && (c.one_click || c.via === "broker")) {
      // One-click is the main path, so the developer app gets its own Connect.
      const mc = h("button", "btn accent sm", "Connect with my app");
      mc.onclick = async () => { mc.disabled = true; const r = await API().ConnectorConnectManual(c.id); mc.disabled = false; if (r && r.status === "error") toast(r.error, "error"); else redraw(); };
      const kr = h("div", "keyrow"); kr.append(mc); fields.append(kr);
    }
    const oneClickish = c.one_click || c.via === "broker";
    if (live || oneClickish) {
      // Already set up, or one click covers it: keep the developer app out of the way.
      const d = h("details", "csetupwrap");
      const label = oneClickish && !(live && c.via !== "broker")
        ? (c.auth === "token" ? "Use your own token instead" : "Use your own developer app instead")
        : (c.auth === "token" ? "Replace the token" : "Developer app and redirect URI");
      d.append(h("summary", "", `${icon("chev", "i sm")}<span>${label}</span>`), bx);
      g.innerHTML = ""; g.append(group(oneClickish ? "Advanced" : c.auth === "token" ? "Token" : "Your developer app").firstChild, d);
    } else g.append(bx);
    setupG = g;
    if (!live && !oneClickish) col.append(g);
  }
  if (c.status === "connected" || c.status === "expired") {
    const g = group("Use with Blaze");
    const bx = h("div", "box pad formgrid");
    const tg = h("label", "toggle", `<input type="checkbox" ${c.enabled ? "checked" : ""}><span><span class="tt">Let Blaze use ${esc(c.name)}</span><span class="td">When off, its tools are hidden from Blaze; the sign-in stays.</span></span>`);
    $("input", tg).onchange = async e => { await API().ConnectorSetEnabled(c.id, e.target.checked); redraw(); };
    bx.append(tg);
    if (c.since) bx.append(h("div", "note2", `Connected ${esc(c.since)}${c.via === "broker" ? " in one click" : ""}${(c.granted || []).length ? ". Blaze can " + esc(c.granted.join(", ")) + "." : "."}`));
    g.append(bx); col.append(g);
  }

  // Tools, grouped (Gmail, Calendar…).
  const groupsSeen = [...new Set((c.tools || []).map(t => t.group))];
  const names = { gmail: "Gmail", calendar: "Calendar", drive: "Drive", contacts: "Contacts" };
  const tg = group("Tools", "Reading runs without asking. Anything that sends, posts or changes things shows an approval card unless you choose Always allow.");
  groupsSeen.forEach(gid => {
    if (groupsSeen.length > 1) tg.append(h("div", "label sub2", esc(names[gid] || gid)));
    tg.append(toolRows(c.tools.filter(t => t.group === gid), c.id, c));
  });
  col.append(tg);
  if (setupG && !setupG.parentNode) col.append(setupG);
}

async function mcpView(el, id) {
  await loadConnectors();
  const s = (CV.mcp || []).find(x => x.id === id);
  if (!s) { show("accounts"); return; }
  const pg = h("div", "page"); const col = h("div", "col"); pg.append(col); el.append(pg);
  const back = h("button", "btn ghost xs back", `<svg class="i sm" viewBox="0 0 16 16"><path d="m10 4-4 4 4 4"/></svg>Accounts`); back.onclick = () => show(acctReturn());
  col.append(back);
  const where = s.transport === "stdio" ? [s.command].concat(s.args || []).join(" ") : s.url;
  const head = h("div", "phead chead", `${cicon(s.transport === "stdio" ? "local" : "mcp", true)}<div class="ph"><div class="label acc">MCP server · ${esc(s.transport === "stdio" ? "local" : s.transport)}</div><h1>${esc(s.name)}</h1><div class="lead mono small">${esc(where)}</div></div>`);
  const acts = h("div", "acts");
  const redraw = () => { if (view === "mcp") show("mcp", id); };
  connRedraw = redraw;
  const rf = h("button", "btn line sm", `${icon("retry", "i sm")}Refresh tools`);
  rf.onclick = async () => { rf.disabled = true; rf.textContent = "Checking…"; const r = await API().MCPRefresh(id); if (r && r.error) toast(r.error, "error"); redraw(); };
  const rm = h("button", "btn ghost sm", "Remove");
  rm.onclick = async () => { if (!rm.dataset.sure) { rm.dataset.sure = 1; rm.textContent = "Remove? Click again"; return; } await API().MCPRemove(id); show("accounts"); };
  acts.append(rf, rm); head.append(acts); col.append(head);
  if (s.error) col.append(h("div", "callout", `${icon("x")}<div><b>Blaze can't reach this server.</b> <span class="mono small">${esc(s.error)}</span></div>`));
  const g = group("Use with Blaze");
  const bx = h("div", "box pad formgrid");
  const t1 = h("label", "toggle", `<input type="checkbox" ${s.enabled ? "checked" : ""}><span><span class="tt">Let Blaze use this server</span><span class="td">${s.transport === "stdio" ? "Blaze starts it when a tool is needed and stops it when Blaze quits." : s.has_token ? "Its token is saved in your system keychain." : "No token saved."}</span></span>`);
  $("input", t1).onchange = async e => { await API().MCPSetEnabled(id, e.target.checked); redraw(); };
  bx.append(t1);
  if (s.checked) bx.append(h("div", "note2", `Checked ${esc(s.checked)} · ${(s.tools || []).length} tools`));
  g.append(bx); col.append(g);
  const tg = group("Tools", "Read-only tools run without asking; destructive ones always ask unless you choose Always allow.");
  if ((s.tools || []).length) tg.append(toolRows(s.tools, "mcp:" + id));
  else tg.append(h("div", "rows", `<div class="empty"><b>No tools listed</b>Refresh once the server is reachable.</div>`));
  col.append(tg);
}

// ---------- toasts ----------
function toast(text, kind) {
  let box = $("#toasts"); if (!box) { box = h("div", ""); box.id = "toasts"; document.body.append(box); }
  const t = h("div", "toast " + (kind || ""), `${kind === "ok" ? icon("check", "i sm") : kind === "error" ? icon("x", "i sm") : '<span class="sq"></span>'}<span>${esc(text)}</span>`);
  box.append(t);
  setTimeout(() => { t.classList.add("out"); setTimeout(() => t.remove(), 300); }, kind === "error" ? 7000 : 3600);
}

// ---------- chat cards ----------
function chatCard(html) {
  const card = h("div", "card conn", html);
  (current ? current.el : $("#log .col", pane)).append(card);
  scrollDown(true);
  return card;
}

on("toast", t => toast(t.text, t.kind));
on("connectors", async () => { await loadConnectors(); refreshConnCount(); if (connRedraw && ["accounts", "connector", "mcp"].includes(view)) connRedraw(); if (view === "settings" && settingsSection === "accounts") show("settings", "accounts"); if (window.Onboard) Onboard.refresh(); });
on("connector_suggest", d => {
  const enable = d.action === "enable";
  const card = chatCard(`<div class="title">${cicon(d.id)}<span class="label acc">${enable ? "Turn on" : "Connect"} ${esc(d.name)}</span></div><div>${esc(d.reason || "")}</div>
    <div class="actions"><button class="btn accent sm go">${enable ? "Turn on" : "Connect"} ${esc(d.name)}</button><button class="btn ghost sm no">Not now</button></div>`);
  $(".go", card).onclick = async () => {
    if (enable) { await API().ConnectorSetEnabled(d.id, true); card.classList.add("done"); $(".actions", card).innerHTML = `<span class="label">Turned on · ask again</span>`; return; }
    await loadConnectors();
    const c = (CV.connectors || []).find(x => x.id === d.id);
    if (!c || (!c.one_click && (c.status === "needs_setup" || c.auth === "token"))) { show("connector", d.id); return; }
    const r = await API().ConnectorConnect(d.id);
    if (r && r.status === "error") toast(r.error, "error"); else { card.classList.add("done"); $(".actions", card).innerHTML = `<span class="label">Finish in your browser</span>`; }
  };
  $(".no", card).onclick = () => card.remove();
});
on("connector_reauth", d => {
  const card = chatCard(`<div class="title">${cicon(d.id)}<span class="label ink">Reconnect ${esc(d.name)}</span></div><div>Your ${esc(d.name)} connection has expired. Reconnect it before Blaze can use it for this request.</div>
    <div class="actions"><button class="btn accent sm go">${icon("retry", "i sm")}Reconnect</button></div>`);
  card.classList.add("reauth");
  $(".go", card).onclick = async () => { const r = await API().ConnectorConnect(d.id); if (r && r.status === "error") { toast(r.error, "error"); show("connector", d.id); } else $(".actions", card).innerHTML = `<span class="label">Finish in your browser, then say “continue”</span>`; };
});
on("connector_consent", d => {
  const card = chatCard(`<div class="title">${cicon(d.id)}<span class="label acc">${esc(d.name)} needs your OK</span></div><div>To do this, Blaze needs permission to ${esc((d.scopes || []).join(", "))}. ${esc(d.name)} will ask you in the browser; nothing else changes.</div>
    <div class="actions"><button class="btn accent sm go">Grant access</button><button class="btn ghost sm no">Not now</button></div>`);
  $(".go", card).onclick = async () => { const r = await API().ConnectorGrant(d.id, d.tool); if (r && r.status === "error") toast(r.error, "error"); else $(".actions", card).innerHTML = `<span class="label">Finish in your browser, then say “continue”</span>`; };
  $(".no", card).onclick = () => card.remove();
});

// /connect in the command menu.
COMMANDS.splice(COMMANDS.findIndex(c => c.name === "jobs"), 0, { name: "connect", desc: "Connect an account or MCP server", sub: async () => {
  await loadConnectors();
  return (CV.connectors || []).map(c => ({ name: c.name, desc: (STATUS[c.status] || STATUS.not_connected)[0] + (c.account ? " · " + c.account : "") + " · " + c.blurb, run: () => show("connector", c.id) }))
    .concat((CV.mcp || []).map(s => ({ name: s.name, desc: "MCP · " + s.status, run: () => show("mcp", s.id) })))
    .concat([{ name: "All accounts…", desc: "Connections, logins, API keys and MCP servers", run: () => show("accounts") }]);
} });

// Tool steps for connector and MCP tools get their plain words from steps.js.

// v0.8.1: Settings › MCP servers. The same servers as Accounts, shared with Blaze CLI
// through ~/.blaze/mcp.json: add, open (tools, approval per tool), turn on or off.
SECTIONS.splice(SECTIONS.findIndex(s => s[0] === "skills"), 0, ["mcp", "MCP servers", "layers"]);
window.SETTINGS_FNS = window.SETTINGS_FNS || {};
window.SETTINGS_FNS.mcp = async (col, redraw) => {
  await loadConnectors();
  const list = CV.mcp || [];
  const g = group("MCP servers", `Tools from Model Context Protocol servers, as <span class="mono">mcp__server__tool</span>. Shared with Blaze CLI in <span class="mono">${esc(CV.mcp_file || "~/.blaze/mcp.json")}</span>: edit either and both see it.`);
  if (CV.mcp_error) g.append(h("div", "pnote bad", esc(CV.mcp_error)));
  const rows = mcpRows(list);
  $$(".r", rows).forEach((r, i) => {
    const s = list[i]; if (!s) return;
    const sw = h("label", "toggle", `<input type="checkbox"${s.enabled ? " checked" : ""}>`);
    sw.title = s.enabled ? "On for Blaze" : "Off for Blaze";
    sw.onclick = e => e.stopPropagation();
    $("input", sw).onchange = async e => { const err = await API().MCPSetEnabled(s.id, e.target.checked); if (err) toast(err, "error"); redraw(); };
    r.append(sw);
  });
  g.append(rows);
  const acts = h("div", "keyrow"); acts.style.marginTop = "10px";
  const add = h("button", "btn line sm", `${icon("plus", "i sm")}Add server`);
  add.onclick = () => { acts.replaceWith(addMcpForm(ok => redraw())); };
  const rf = h("button", "btn ghost sm", `${icon("retry", "i sm")}Check all`);
  rf.onclick = async () => { rf.disabled = true; rf.textContent = "Checking…"; for (const s of list.filter(x => x.enabled)) await API().MCPRefresh(s.id); redraw(); };
  acts.append(add, rf); g.append(acts);
  col.append(g);
};
