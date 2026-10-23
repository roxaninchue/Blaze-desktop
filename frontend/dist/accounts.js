// Accounts: one screen for everything Blaze can sign in to, with square tabs
// All / Connections / Logins / API Keys. Connections sign in through the browser
// (OAuth or a device code) plus your MCP servers; Logins take a token; API Keys are
// the model providers. Sign-ins, tokens and keys live in the OS keychain.
// Loaded after app.js and connectors.js.

const ATABS = [["all", "All"], ["connections", "Connections"], ["logins", "Logins"], ["keys", "API Keys"]];
let acctTab = "all";
let acctFrom = "";   // "setup" when a row was opened from onboarding
const acctReturn = () => { const r = acctFrom || "accounts"; acctFrom = ""; return r; };
window.VIEWS = window.VIEWS || {};

const isLogin = c => c.auth === "token";
const capOf = c => (BRAND[c.id] && BRAND[c.id].cap) || c.blurb || "";

// Right-hand side of a row: status + one button.
function acctStatus(text, cls, title) {
  return `<span class="astat" title="${esc(title || text)}"><span class="dot ${cls}"></span><span>${esc(text)}</span></span>`;
}

// One connector row (also used by onboarding with opts.from = "setup").
function connectorRow(c, redraw, opts = {}) {
  const r = h("div", "arow click");
  const st = c.status;
  let stat = "";
  if (st === "connected" && c.enabled) stat = acctStatus("Connected", "", c.account ? "Connected as " + c.account : "Connected");
  else if (st === "connected") stat = acctStatus("Off", "off", "Connected, but off for Blaze");
  else if (st === "expired") stat = acctStatus("Expired", "ink");
  else if (st === "connecting") stat = acctStatus("Waiting", "warn", "Finish signing in in your browser");
  else if (c.one_click) stat = acctStatus("One click", "", "Connect signs you in through the browser. No keys needed.");
  else if (c.cloud === "soon") stat = acctStatus("Own key", "off", "One-click Connect is coming soon; connect with your own key for now");
  const cap = st === "connected" && c.account ? `${esc(c.account)} · ${esc(capOf(c))}` : esc(capOf(c));
  r.innerHTML = `${logo(c.id)}<div class="am"><div class="an">${esc(c.name)}</div><div class="ac">${cap}</div></div><div class="ar">${stat}</div>`;
  const open = () => { acctFrom = opts.from || ""; show("connector", c.id); };
  const b = connAction(c, redraw);
  // Quiet buttons: Connect and Manage are line buttons, Reconnect stays accent.
  if (b.textContent === "Connect" || b.textContent === "Set up") { b.className = "btn line sm"; b.textContent = "Connect"; }
  if (b.textContent === "Manage") { b.className = "btn ghost sm"; b.innerHTML = `Manage${icon("chev", "i sm")}`; }
  if (st === "needs_setup" || isLogin(c) && !c.one_click && st !== "connected" && st !== "expired" || st === "connected") b.onclick = e => { e.stopPropagation(); open(); };
  $(".ar", r).append(b);
  r.onclick = e => { if (!e.target.closest("button")) open(); };
  // Anything that leaves for the connector page comes back to where it started.
  r.addEventListener("click", () => { acctFrom = opts.from || ""; }, true);
  return r;
}

function mcpRow(s) {
  const st = { ready: ["", `${(s.tools || []).length} tools`], error: ["ink", "Can't connect"], unchecked: ["warn", "Not checked"], disabled: ["off", "Off"] }[s.status] || ["off", ""];
  const where = s.transport === "stdio" ? [s.command].concat(s.args || []).join(" ") : s.url;
  const r = h("div", "arow click", `${logo(s.transport === "stdio" ? "local" : "mcp")}<div class="am"><div class="an">${esc(s.name)}</div><div class="ac mono">${esc(s.transport === "stdio" ? "Local · " : "Remote · ")}${esc(where)}</div></div><div class="ar">${acctStatus(st[1], st[0])}</div>`);
  const m = h("button", "btn ghost sm", `Manage${icon("chev", "i sm")}`);
  $(".ar", r).append(m);
  r.onclick = () => show("mcp", s.id);
  return r;
}

// A model provider row; Manage / Add key expands the full key panel in place.
function keyRow(p, redraw) {
  const wrap = h("div", "aitem");
  const r = h("div", "arow");
  const cap = p.ready ? `${p.model ? `<span class="mono">${esc(p.model)}</span>` : "No model chosen"}${p.free && !p.keyless ? " · Free tier" : ""}`
    : p.keyless ? "Runs on this computer, no key" : p.id === "custom" ? "Any OpenAI-compatible service" : `${p.free ? "Free tier" : "Paid"}${p.key_hint && p.key_hint !== "…" ? ` · Key starts <span class="mono">${esc(p.key_hint)}</span>` : " · Bring your own key"}`;
  const stat = p.ready ? acctStatus(p.keyless ? "Local" : p.keys > 1 ? p.keys + " keys" : "Connected", "") : "";
  r.innerHTML = `${logo(p.id)}<div class="am"><div class="an">${esc(p.short)}</div><div class="ac">${cap}</div></div><div class="ar">${stat}</div>`;
  const b = h("button", p.ready ? "btn ghost sm" : "btn line sm", p.ready ? `Manage${icon("down", "i sm")}` : p.keyless ? "Connect" : "Add key");
  const x = h("div", "axp"); x.hidden = true;
  const toggle = () => { x.hidden = !x.hidden; wrap.classList.toggle("open", !x.hidden); if (!x.dataset.built) { x.dataset.built = 1; buildProviderPanel(x, p, redraw); } };
  b.onclick = e => { e.stopPropagation(); toggle(); };
  r.onclick = e => { if (!e.target.closest("button")) toggle(); };
  r.classList.add("click");
  $(".ar", r).append(b);
  wrap.append(r, x);
  return wrap;
}

function acctSection(title, n, right) {
  const s = h("div", "asec", `<span class="label">${esc(title)}</span>${n != null ? `<span class="label weak">${pad(n)}</span>` : ""}<span class="grow"></span>`);
  if (right) s.append(right);
  return s;
}

async function drawAccounts(body, redraw) {
  await loadConnectors(); refreshConnCount();
  const conns = CV.connectors || [], mcps = CV.mcp || [];
  const oauth = conns.filter(c => !isLogin(c)), logins = conns.filter(isLogin);
  const provs = (S.providers || []).filter(p => p.id !== "pollinations").sort((a, b) => (b.ready ? 1 : 0) - (a.ready ? 1 : 0));
  const list = h("div", "alist");
  const t = acctTab;
  if (t === "all" && window.accountCard) {
    try { await loadAccount(); list.append(ACCT.signed_in ? accountCard(true) : signedOutCard()); } catch (e) { /* account service is optional */ }
  }
  const box = () => { const b = h("div", "abox"); list.append(b); return b; };
  if (t === "all" || t === "connections") {
    if (t === "all") list.append(acctSection("Connections", oauth.length));
    const b = box(); oauth.forEach(c => b.append(connectorRow(c, redraw)));
    const add = h("button", "btn ghost xs", `${icon("plus", "i sm")}Add server`);
    list.append(acctSection("My MCPs", mcps.length, add));
    const slot = h("div"); list.append(slot);
    add.onclick = () => { slot.innerHTML = ""; slot.append(addMcpForm((ok, s) => { slot.innerHTML = ""; if (ok && s) show("mcp", s.id); else if (ok) redraw(); })); };
    const mb = box(); mcps.forEach(s => mb.append(mcpRow(s)));
    if (!mcps.length) mb.append(h("div", "aempty", "Add a remote server by URL, or a local one by its command. Its tools go through the same approval cards."));
  }
  if (t === "all" || t === "logins") {
    list.append(acctSection("Logins", logins.length));
    const b = box(); logins.forEach(c => b.append(connectorRow(c, redraw)));
    if (t === "logins") list.append(h("div", "anote", "Logins take a token or integration secret you make on the service's site. Blaze checks it, then keeps it in your system keychain."));
  }
  if (t === "all" || t === "keys") {
    list.append(acctSection("API keys", provs.filter(p => p.ready).length + " of " + provs.length));
    const b = box(); provs.forEach(p => b.append(keyRow(p, redraw)));
    if (t === "keys") list.append(h("div", "anote", "Model providers. A second key for the same service joins its key ring: when one runs out of quota, Blaze carries on with the next."));
  }
  list.append(h("div", "anote", `Sign-ins come back to <span class="mono">${esc(CV.loopback || "127.0.0.1")}</span>. Tokens and keys stay in your system keychain, never in a file. One-click Connect signs in through Blaze's sign-in service, which never keeps your tokens.`));
  body.innerHTML = ""; body.append(list);
}

function acctTabs(onPick) {
  const tabs = h("div", "atabs"); tabs.setAttribute("role", "tablist");
  ATABS.forEach(([id, name]) => {
    const b = h("button", id === acctTab ? "on" : "", esc(name));
    b.setAttribute("role", "tab"); b.setAttribute("aria-selected", id === acctTab);
    b.onclick = () => { acctTab = id; $$("button", tabs).forEach(x => { x.classList.toggle("on", x === b); x.setAttribute("aria-selected", x === b); }); onPick(); };
    tabs.append(b);
  });
  return tabs;
}

function accountsView(el, tabArg) {
  if (tabArg && ATABS.some(t => t[0] === tabArg)) acctTab = tabArg;
  const pg = h("div", "page acctpage"); const col = h("div", "col"); pg.append(col); el.append(pg);
  col.append(h("div", "phead", `<div class="ph"><div class="label acc">Accounts</div><h1>Accounts</h1><p class="lead">What Blaze can use for you: your apps, logins, MCP servers and model keys.</p></div>`));
  const body = h("div");
  const redraw = async () => { if (view === "accounts") await drawAccounts(body, redraw); };
  col.append(acctTabs(redraw), body);
  connRedraw = redraw;
  drawAccounts(body, redraw);
}

window.VIEWS.accounts = accountsView;
window.VIEWS.connectors = accountsView; // old name (PR #6, /connectors)
// Detail pages close the onboarding overlay; their back button returns to it.
window.VIEWS.connector = (el, id) => { if (window.Onboard) Onboard.close(); return connectorView(el, id); };
window.VIEWS.mcp = mcpView;

// Settings › Accounts draws the same screen.
SECTIONS.splice(SECTIONS.findIndex(s => s[0] === "skills"), 0, ["accounts", "Accounts", "plug"]);
window.SETTINGS_FNS = window.SETTINGS_FNS || {};
window.SETTINGS_FNS.accounts = async (col, redraw) => {
  const body = h("div");
  col.append(acctTabs(() => drawAccounts(body, redraw)), body);
  await drawAccounts(body, redraw);
};

COMMANDS.splice(COMMANDS.findIndex(c => c.name === "update"), 0, { name: "accounts", desc: "Connections, logins, API keys and MCP servers", run: () => show("accounts") });
