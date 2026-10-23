// Private chat (v0.19.1, Blaze Mobile's private chat). The ghost button in the top bar opens a
// chat that saves nothing: no chat file, history, title, memories or skills (private_app.go).
// A "Private" pill replaces Home | Chat | Projects. Leaving (ghost, pill, Esc or any other
// view) asks "Leave private chat?" and throws it away.
const Private = (() => {
  const GHOST = '<svg class="i" viewBox="0 0 16 16"><path d="M3.5 14V7a4.5 4.5 0 0 1 9 0v7l-1.5-1.2L9.5 14 8 12.8 6.5 14 5 12.8z"/><circle cx="6.3" cy="7" r=".6"/><circle cx="9.7" cy="7" r=".6"/></svg>';
  let on = false;

  function hero() {
    const col = $("#log .col", pane); if (!col) return;
    col.innerHTML = "";
    col.append(h("div", "hero privhero", `<div class="ghostbig">${GHOST.replace('class="i"', 'class="i" style="width:56px;height:56px"')}</div><div class="big">Private chat</div><p class="lead">Nothing here is saved or remembered. It's gone when you leave.</p>`));
  }

  async function enter() {
    if (on) return leave();
    if (typeof leaveTurn === "function") leaveTurn();
    if (S && S.project && window.OneChat) await OneChat.toMain();
    const id = await API().OpenPrivate();
    convId = id; on = true;
    document.body.classList.add("private");
    origShow("work");
    hero();
    const ta = $("#input"); if (ta) ta.focus();
  }

  const hasTurns = () => !!$("#log .col .msg", pane);

  // ask shows "Leave private chat?"; then() runs after leaving.
  function leave(then) {
    if (!on) { if (then) then(); return; }
    if (!hasTurns()) { drop().then(() => then && then()); return; }
    const d = h("div", "privdlg", `<div class="card" role="dialog" aria-modal="true"><div class="tt">Leave private chat?</div><p>This chat isn't saved. It will be gone for good.</p><div class="acts"><button class="btn line sm st">Stay</button><button class="btn accent sm lv">Leave</button></div></div>`);
    const close = () => d.remove();
    $(".st", d).onclick = close;
    d.onclick = e => { if (e.target === d) close(); };
    $(".lv", d).onclick = async () => { close(); await drop(); if (then) then(); };
    document.body.append(d);
    $(".lv", d).focus();
  }

  async function drop() {
    on = false;
    document.body.classList.remove("private");
    if (typeof leaveTurn === "function") leaveTurn();
    await API().LeavePrivate();
    if (window.OneChat) await OneChat.openMain();
  }

  // Any other view leaves the private chat first.
  const origShow = window.show;
  window.show = function (v, ...rest) {
    if (on && v !== "work") { leave(() => origShow(v, ...rest)); return; }
    return origShow(v, ...rest);
  };

  function build() {
    const top = $("#top"); if (!top) return;
    const b = h("button", "chip shghost", GHOST); b.id = "privateBtn"; b.title = "Private chat: nothing is saved";
    b.onclick = e => { e.stopPropagation(); enter(); };
    const sb = $("#searchBtn"); if (sb) sb.before(b); else top.append(b);
    const pp = h("button", "tabs shpill on", `${GHOST}<span>Private</span>`); pp.id = "privPill"; pp.title = "Leave private chat";
    pp.onclick = () => leave();
    const pill = $("#pill"); if (pill) pill.after(pp); else top.append(pp);
    addEventListener("keydown", e => { if (on && e.key === "Escape" && !document.querySelector(".privdlg")) { e.preventDefault(); e.stopImmediatePropagation(); leave(); } }, true);
  }

  build();
  return { enter, leave, get on() { return on; } };
})();
window.Private = Private;
