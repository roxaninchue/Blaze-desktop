// Settings › Account › Sync across devices (cloudsync_app.go). Loaded after account.js; it only
// uses app.js / account.js globals (API, on, $, h, esc, group, toast, ACCT, convId, busy, view,
// loadConv) and wraps SETTINGS_FNS.account, so the rest of Settings is untouched.
(() => {
  const base = window.SETTINGS_FNS && window.SETTINGS_FNS.account;
  if (!base) return;
  let mounted = null; // { render(view) } while the Account page is open

  window.SETTINGS_FNS.account = async (col, redraw) => {
    await base(col, redraw);
    if (typeof ACCT === "undefined" || !ACCT || !ACCT.signed_in) { mounted = null; return; }
    const g = syncGroup();
    const danger = [...col.querySelectorAll(".group")].find(x => x.querySelector(".dangerrow"));
    col.insertBefore(g, danger || null);
  };

  const when = s => new Date(s * 1000).toLocaleString("en-GB", { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" });

  function syncGroup() {
    const g = group("Sync across devices", "Your memory and chats follow your Blaze account to Blaze on your phone, your other computers and Blaze on the web. They're encrypted on this computer with a passphrase only you know, so Blaze's service only ever stores scrambled copies. Private chats never sync.");
    const bx = h("div", "box pad syncbox");
    const row = h("label", "toggle", `<input type="checkbox"><span><span class="tt">Sync across devices</span><span class="td" style="display:block">Memory, plus the newest 300 messages of each chat right away. Older messages follow a few hundred at a time.</span></span>`);
    const cb = $("input", row);
    const step = h("div", "keyrow syncstep");
    step.hidden = true;
    step.innerHTML = `<input class="field" type="password" autocomplete="new-password" placeholder="Sync passphrase (8+ characters)"><button class="btn accent sm">Turn on</button><button class="btn ghost sm">Cancel</button>`;
    const [inp, go, cancel] = [$("input", step), ...step.querySelectorAll("button")];
    const hint = h("div", "note2 small", "Use the same passphrase on every device. It's never sent anywhere or saved; Blaze keeps only the key made from it, in this computer's keychain. If you forget it, your synced copy can't be opened.");
    hint.hidden = true;
    const st = h("div", "out small sub", "");
    const acts = h("div", "keyrow");
    const now = h("button", "btn line sm", "Sync now");
    acts.append(st, now);

    let cur = null;
    const render = v => {
      cur = v || cur;
      if (!cur) return;
      cb.checked = !!cur.on;
      cb.disabled = !cur.signedIn;
      const ask = cur.on && cur.needPassphrase;
      if (ask) { step.hidden = false; hint.hidden = false; go.textContent = "Unlock"; cancel.hidden = true; }
      else if (cur.on) { step.hidden = true; hint.hidden = true; cancel.hidden = false; }
      now.hidden = !cur.on || ask;
      now.disabled = !!cur.running;
      let line = cur.status || "";
      if (cur.on && cur.lastSync && !cur.running && cur.error) line += " Last good sync " + when(cur.lastSync) + ".";
      st.className = "out small " + (cur.error && !cur.running ? "bad" : "sub");
      st.textContent = line;
    };

    cb.onchange = async () => {
      const want = cb.checked;
      if (want) { cb.checked = false; step.hidden = false; hint.hidden = false; go.textContent = "Turn on"; cancel.hidden = false; inp.focus(); return; }
      cb.disabled = true;
      const v = await API().SyncDisable();
      render(v);
      toast("Sync is off on this computer. Nothing was deleted.");
    };
    cancel.onclick = () => { step.hidden = true; hint.hidden = true; inp.value = ""; render(); };
    const submit = async () => {
      const pass = inp.value;
      if (pass.trim().length < 8) { st.className = "out small bad"; st.textContent = "Use a passphrase of at least 8 characters."; return; }
      go.disabled = true; inp.disabled = true;
      st.className = "out small sub"; st.textContent = "Checking the passphrase…";
      const v = await API().SyncEnable(pass);
      go.disabled = false; inp.disabled = false;
      if (v && v.error && !v.on) { st.className = "out small bad"; st.textContent = v.error; return; }
      if (v && v.error && v.needPassphrase) { st.className = "out small bad"; st.textContent = v.error; return; }
      inp.value = ""; step.hidden = true; hint.hidden = true;
      render(v);
    };
    go.onclick = submit;
    inp.onkeydown = e => { if (e.key === "Enter") submit(); };
    now.onclick = async () => { now.disabled = true; render(await API().SyncNow()); };

    bx.append(row, step, hint, acts);
    g.append(bx);
    mounted = { render, el: g };
    API().SyncState().then(render).catch(() => {});
    return g;
  }

  on("sync:state", v => { if (mounted && document.body.contains(mounted.el)) mounted.render(v); });
  // A chat changed by a sync round: show it again if it's the one on screen and idle.
  on("sync:chats", async ids => {
    try {
      if (!ids || !convId || !ids.includes(convId) || busy || (view !== "work" && view !== "design")) return;
      const c = await API().OpenConversation(convId);
      if (c && c.id === convId && !busy) loadConv(c);
    } catch (e) {}
  });
})();
