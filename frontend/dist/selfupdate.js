// /update: Blaze updates itself right in the chat, no trip to Settings.
// One Blaze message with the live line ("Checking for a new version" → "Downloading
// Blaze v0.4.8 · 42%" → "Installing v0.4.8" → "Restarting Blaze"), then the app
// restarts by itself. Also runs when the model calls update_blaze ("update yourself").
const SelfUpdate = (() => {
  let live = null;
  const sleep = ms => new Promise(r => setTimeout(r, ms));

  function message() {
    const col = $("#log .col", pane);
    const hero = col && $(".hero", col); if (hero) hero.remove();
    const el = h("div", "msg blaze selfupd");
    el.append(h("div", "who", `<span class="mk"></span><span class="label">Blaze</span>`));
    const workEl = h("div", "work", `<span class="sq"></span><span class="wt"></span>`);
    const mdEl = h("div", "md");
    el.append(workEl, mdEl);
    if (col) col.append(el);
    return { el, workEl, mdEl, order: [], started: Date.now() };
  }

  function say(t, text) { t.mdEl.innerHTML = MD.render(text); scrollDown(); }

  // The finished message: the answer, optional buttons, and no live line.
  function end(t, text, buttons) {
    t.finished = true; if (live === t) live = null;
    if (t.bar) { t.bar.remove(); t.bar = null; }
    t.workEl.style.display = "none"; t.workEl.classList.remove("live");
    say(t, text);
    if (buttons && buttons.length) {
      const row = h("div", "actions");
      buttons.forEach(([label, fn, cls]) => { const b = h("button", "btn sm " + (cls || "line"), esc(label)); b.onclick = () => { row.remove(); fn(); }; row.append(b); });
      t.mdEl.append(row);
    }
    updBusy = false; scrollDown(true);
  }

  function agentsLine() {
    const c = window.Agents && Agents.counts ? Agents.counts() : { total: 0 };
    const n = c.total || 0;
    return n ? ` Your ${n === 1 ? "background agent pauses" : n + " background agents pause"} for a moment and ${n === 1 ? "picks" : "pick"} up where ${n === 1 ? "it" : "they"} left off.` : "";
  }

  async function run() {
    if (live && document.body.contains(live.el)) { live.el.scrollIntoView({ block: "nearest", behavior: "smooth" }); return; }
    if (view !== "work" && view !== "design") show("work");
    const t = live = message();
    try {
      if (busy) { setStatus(t, "Waiting for this answer to finish"); while (busy) await sleep(400); }
      setStatus(t, "Checking for a new version");
      const u = await checkUpdate(false);
      if (!u || u.error) return end(t, `I couldn't check for an update. ${(u && u.error) || ""}`.trim(), [["Try again", run, "accent"]]);
      if (!u.available) return end(t, `You're on the newest version, **${u.current}**.`);
      if (!u.can_auto) return end(t, `Blaze **${u.latest}** is out, but I can't install it on this computer by myself. ${u.method || ""}`.trim(), [["Open the downloads page", () => API().OpenURL(u.page), "accent"]]);
      t.latest = u.latest;
      say(t, `Updating you from ${u.current} to **${u.latest}**. Blaze restarts by itself when it's installed.${agentsLine()}`);
      updBusy = true;
      setStatus(t, `Downloading Blaze ${u.latest}`);
      let err = "";
      try { err = await API().InstallUpdate(); } catch (e) { err = String(e); }
      if (err) {
        const [m, d] = updSplit(err);
        // A refused (unsigned) update is final for this release: no "Try again".
        end(t, d ? m : `The update stopped: ${m}`, d ? [] : [["Try again", run, "accent"]]);
        if (d) t.mdEl.insertAdjacentHTML("beforeend", updDetails(d));
      }
      // "" means Blaze is about to restart; the line stays on "Restarting Blaze".
    } catch (e) {
      end(t, `The update stopped: ${e}`, [["Try again", run, "accent"]]);
    }
  }

  // Download and install progress from update.go.
  on("update", p => {
    const t = live; if (!t || t.finished || !p) return;
    const v = t.latest || "";
    // v0.18.1: a BrandProgress bar under the live line while the download runs
    if (p.stage === "download") {
      if (!t.bar) { t.bar = BrandProgress.create(-1); t.workEl.after(t.bar); }
      BrandProgress.set(t.bar, p.pct > 0 ? p.pct : -1);
    } else if (t.bar) { t.bar.remove(); t.bar = null; }
    if (p.stage === "download") setStatus(t, p.pct > 0 ? `Downloading Blaze ${v} · ${p.pct}%` : `Downloading Blaze ${v}`);
    else if (p.stage === "verify") setStatus(t, `Checking the signature on ${v}`);
    else if (p.stage === "install") setStatus(t, `Installing ${v}`);
    else if (p.stage === "restart") setStatus(t, "Restarting Blaze");
  });
  // The model called update_blaze: same flow, after its short reply finishes.
  on("selfupdate", () => run());

  // /update now updates Blaze instead of opening Settings.
  const cmd = COMMANDS.find(c => c.name === "update");
  if (cmd) { cmd.desc = "Update Blaze to the newest version"; cmd.run = () => { run(); }; }

  return { run };
})();
