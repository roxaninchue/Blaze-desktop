// v0.19 The Store and the Downloads list, shared by Blaze Music and Blaze Video (mobile
// StoreUi.kt / DownloadsUi.kt). Sources: YouTube, Jamendo (free client_id), Internet Archive
// (CC / public domain), public-domain films, Blender open movies; a pasted link works in any
// search box. Every item shows its licence; Play streams through Blaze, Download keeps a copy.
const BlazeMediaStore = (() => {
  const M = () => window.BlazeMusic;
  const mi = (n, c) => M().mi(n, c);
  const st = { audio: { src: "youtube", q: "", items: [], next: "", err: "", setup: "", busy: false }, video: { src: "youtube", q: "", items: [], next: "", err: "", setup: "", busy: false } };

  async function view(el, kind, opts) {
    const s = st[kind];
    let sources = [];
    try { sources = await API().MediaSources(kind) || []; } catch (e) {}
    if (!sources.find(x => x.id === s.src) && sources.length) s.src = sources[0].id;
    const src = sources.find(x => x.id === s.src) || {};
    el.innerHTML = `<div class="mzbar"><form class="mzsearch big">${mi("search", "i sm")}<input placeholder="Search ${esc(src.name || "the Store")} or paste a link" value="${esc(s.q)}"><button class="btn accent xs" type="submit">Search</button></form>
      <div class="mzchips src">${sources.map(x => `<button class="mzchip ${x.id === s.src ? "on" : ""}" data-s="${x.id}">${esc(x.name)}${x.setup ? '<span class="dot"></span>' : ""}</button>`).join("")}
      <button class="mzchip ghost" data-link="1">${mi("link", "i sm")}Download by link</button></div></div><div class="mzres"></div>`;
    const form = $("form", el), inp = $("input", el), res = $(".mzres", el);
    form.onsubmit = e => { e.preventDefault(); s.q = inp.value.trim(); s.items = []; s.next = ""; run(res, kind, opts, false); };
    $$("[data-s]", el).forEach(b => b.onclick = () => { s.src = b.dataset.s; s.items = []; s.next = ""; s.err = ""; s.setup = ""; view(el, kind, opts); });
    $("[data-link]", el).onclick = () => linkDialog(kind, opts);
    if (!s.items.length && !s.err && !s.setup && (s.q || src.browsable)) run(res, kind, opts, false);
    else paint(res, kind, opts, src);
  }
  async function run(res, kind, opts, more) {
    const s = st[kind];
    s.busy = true; if (!more) { s.err = ""; s.setup = ""; } paint(res, kind, opts);
    let r = null;
    try { r = await API().MediaStoreSearch(kind, s.src, s.q, more ? s.next : ""); } catch (e) { r = { error: String(e), items: [] }; }
    s.busy = false;
    if (!r) r = { items: [] };
    s.err = r.error || ""; s.setup = r.setup || ""; s.next = r.next || "";
    s.items = more ? s.items.concat(r.items || []) : (r.items || []);
    if (r.link && s.items.length === 1) open(s.items[0], kind, opts);
    if (res.isConnected) paint(res, kind, opts);
  }
  async function paint(res, kind, opts) {
    const s = st[kind];
    res.innerHTML = "";
    if (s.setup) { jamendoSetup(res, kind, opts); return; }
    if (!s.items.length && !s.busy) {
      if (s.err) res.append(h("div", "mzempty", `<b>${esc(s.err)}</b>`));
      let ideas = [];
      try { ideas = await API().MediaIdeas(kind) || []; } catch (e) {}
      if (!res.isConnected) return;
      res.append(h("div", "mzlh", `<span class="t">${kind === "video" ? "Ideas for you" : "Try"}</span>`));
      const box = h("div", "mzideas");
      ideas.forEach(i => { const b = h("button", "mzchip idea", esc(i)); b.onclick = () => { s.q = i; const inp = res.parentNode.querySelector(".mzsearch input"); if (inp) inp.value = i; s.items = []; run(res, kind, opts, false); }; box.append(b); });
      res.append(box);
      res.append(h("p", "mzsmall", kind === "video" ? "YouTube videos stream for personal viewing. Public-domain films and the Blender open movies are free to keep and share." : "YouTube songs are for personal listening. Jamendo and the Internet Archive are Creative Commons or public domain: free to keep and share under their licence."));
      return;
    }
    const grid = h("div", kind === "video" ? "mzvgrid" : "mzsgrid");
    s.items.forEach(it => {
      const c = h("button", "mzitem" + (it.square ? " sq" : ""), `<span class="th">${it.thumb ? `<img src="${esc(it.thumb)}" alt="" loading="lazy" onerror="this.remove()">` : mi(kind === "video" ? "film" : "note")}${it.duration_text ? `<span class="dur">${esc(it.duration_text)}</span>` : ""}</span>
        <span class="t">${esc(it.title)}</span><span class="s">${esc([it.author, it.views].filter(Boolean).join(" \u00b7 "))}</span>${it.licence ? `<span class="pill ${/CC|public/i.test(it.licence) ? "acc" : ""}">${esc(it.licence)}</span>` : ""}`);
      c.onclick = () => open(it, kind, opts);
      grid.append(c);
    });
    res.append(grid);
    if (s.busy) res.append(h("div", "mzsmall center", `<blaze-emblem size="22" live></blaze-emblem> Searching\u2026`));
    else if (s.next) { const m = h("button", "btn line sm mzmore", "More results"); m.onclick = () => run(res, kind, opts, true); res.append(m); }
    if (s.err && s.items.length) res.append(h("div", "mzsmall", esc(s.err)));
  }
  function jamendoSetup(res, kind, opts) {
    const card = h("div", "mzhero glass col");
    card.innerHTML = `<div class="t">Jamendo needs a free client_id</div><p class="mzsmall">Blaze can get it for you: ask Blaze \u201cset up the Jamendo Store\u201d. Or make one at <b>devportal.jamendo.com</b> (My applications \u2192 new app) and paste its Client ID here.</p>
      <form class="mzacts"><input class="field mzfield" placeholder="Client ID"><button class="btn accent sm" type="submit">Save</button><button class="btn line sm ask" type="button">Ask Blaze to set it up</button></form>`;
    $("form", card).onsubmit = async e => { e.preventDefault(); const v = $("input", card).value.trim(); if (!v) return; const r = await API().MediaJamendoSet(v); M().toast(r, 4000); if (/^Saved/.test(r)) { st[kind].setup = ""; run(res, kind, opts, false); } };
    $(".ask", card).onclick = () => { if (typeof show === "function") { show("work"); const i = $("#input"); if (i) { i.value = "Set up the Jamendo Store for Blaze Music (get a free client_id at devportal.jamendo.com and save it with jamendo_setup)."; i.focus(); } } };
    res.append(card);
  }
  function linkDialog(kind, opts) {
    const f = h("input", "field mzfield"); f.placeholder = "https://\u2026 (YouTube, or a link to a music or video file)";
    const box = h("div", "mzform"); box.append(h("p", "mzsmall", "Paste a YouTube link, a direct link to an audio or video file, or a page with a video on it."), f);
    M().dialog("Download by link", box, [["Cancel", () => {}, "ghost"], ["Open", () => { const v = f.value.trim(); if (!v) return false; st[kind].q = v; resolveLink(v, kind, opts); }, "accent"]]);
    setTimeout(() => f.focus(), 50);
  }
  async function resolveLink(v, kind, opts) {
    const r = await API().MediaStoreSearch(kind, "link", v, "");
    if (r && r.items && r.items[0]) open(r.items[0], kind, opts); else M().toast((r && r.error) || "That link didn't work.", 4000);
  }

  // The item sheet: licence, qualities, what streaming costs, Play / Download.
  async function open(it, kind, opts) {
    const box = h("div", "mzitemsheet");
    box.innerHTML = `<div class="mzis-top"><span class="th ${it.square ? "sq" : ""}">${it.thumb ? `<img src="${esc(it.thumb)}" alt="">` : mi(kind === "video" ? "film" : "note")}</span>
      <div class="m"><div class="t">${esc(it.title)}</div><div class="s">${esc([it.author, it.duration_text].filter(Boolean).join(" \u00b7 "))}</div>${it.licence ? `<span class="pill ${/CC|public/i.test(it.licence) ? "acc" : ""}">${esc(it.licence)}</span>` : ""}</div></div>
      <div class="mzis-q"><div class="mzsmall center"><blaze-emblem size="22" live></blaze-emblem> Getting the qualities\u2026</div></div>`;
    const d = M().dialog(kind === "video" ? "Video" : "Song", box, []);
    let v = null;
    try { v = await API().MediaStoreOpen(kind, it); } catch (e) { v = { error: String(e) }; }
    if (!box.isConnected) return;
    const q = $(".mzis-q", box);
    if (!v || v.error) { q.innerHTML = `<div class="mzerr">${esc((v && v.error) || "Couldn't open it.")}</div>`; return; }
    let pick = v.default || (v.choices[0] && v.choices[0].id);
    q.innerHTML = `<div class="label">Download quality</div><div class="mzqual">${v.choices.map(c => `<label class="mzq"><input type="radio" name="mzq" value="${esc(c.id)}" ${c.id === pick ? "checked" : ""}><span class="t">${esc(c.label)}</span><span class="s">${esc(c.detail || "")}</span></label>`).join("") || '<div class="mzsmall">Nothing downloadable.</div>'}</div>
      ${v.note ? `<p class="mzsmall">${esc(v.note)}</p>` : ""}${v.cost ? `<p class="mzsmall">${esc(v.cost)}</p>` : ""}${v.detail.licence_url ? `<p class="mzsmall">Licence: ${esc(v.detail.licence || "")} \u00b7 ${esc(v.detail.licence_url)}</p>` : ""}`;
    $$("input", q).forEach(r => r.onchange = () => { pick = r.value; });
    const acts = $(".mzsh-a", d.el);
    if (v.stream) { const p = h("button", "btn line", `${mi("play", "i sm")}${kind === "video" ? "Watch" : "Play"}`); p.onclick = () => { d.close(); opts.play(it, v); }; acts.append(p); }
    if (v.choices.length) {
      const b = h("button", "btn accent", `${mi("dl", "i sm")}Download`);
      b.onclick = async () => { b.disabled = true; const err = await API().MediaStoreDownload(kind, it, pick); if (err) { M().toast(err, 4000); b.disabled = false; return; } d.close(); M().toast(kind === "video" ? "Downloading to Videos" : "Downloading to Music"); };
      acts.append(b);
    }
  }

  // ---------------------------------------------------------------- downloads
  function downloads(el, kind) {
    el.innerHTML = `<div class="mzdl"></div>`;
    const box = $(".mzdl", el);
    const draw = async () => {
      if (!box.isConnected) { window.removeEventListener("blazemedia:downloads", draw); return; }
      let jobs = [];
      try { jobs = (await API().MediaDownloads() || []).filter(j => j.kind === kind); } catch (e) {}
      box.innerHTML = "";
      if (!jobs.length) { box.append(h("div", "mzempty", `<b>No downloads</b>${kind === "video" ? "Videos" : "Songs"} you download from the Store, or ask Blaze to download, appear here and land in your ${kind === "video" ? "Videos" : "Music"} folder.`)); return; }
      const done = jobs.filter(j => j.st === "DONE" || j.st === "CANCELED");
      if (done.length) { const c = h("button", "btn ghost xs mzclear", "Clear finished"); c.onclick = () => { done.forEach(j => API().MediaDlDismiss(j.id)); }; box.append(c); }
      jobs.slice().reverse().forEach(j => {
        const pct = j.pct || 0, act = j.st === "QUEUED" || j.st === "RUNNING";
        const line = j.st === "RUNNING" ? `${pct}%` : j.st === "QUEUED" ? (j.next > Date.now() ? "Retrying soon" + (j.err ? ` \u00b7 ${j.err}` : "") : "Waiting") : j.st === "PAUSED" ? `Paused \u00b7 ${pct}%` : j.st === "FAILED" ? `Failed \u00b7 ${j.err || ""}` : j.st === "DONE" ? "Saved" : "Canceled";
        const r = h("div", "mzdlrow" + (j.st === "FAILED" ? " bad" : ""));
        r.innerHTML = `<span class="th">${j.thumb ? `<img src="${esc(j.thumb)}" alt="" onerror="this.remove()">` : mi(kind === "video" ? "film" : "note")}</span>
          <span class="m"><span class="t">${esc(j.title)}</span><span class="s">${esc([j.q, line].filter(Boolean).join(" \u00b7 "))}</span>${act || j.st === "PAUSED" ? `<span class="mzbar2"><i style="width:${pct}%"></i></span>` : ""}</span>
          <span class="a"></span>`;
        const a = $(".a", r);
        const btn = (label, ic, fn) => { const b = h("button", "mzround ghost sm", mi(ic, "i sm")); b.title = label; b.setAttribute("aria-label", label); b.onclick = fn; a.append(b); };
        if (act) btn("Pause", "pause", () => API().MediaDlPause(j.id));
        if (j.st === "PAUSED" || j.st === "FAILED") btn("Resume", "retry", () => API().MediaDlResume(j.id));
        if (j.st === "DONE" && kind === "audio") btn("Play", "play", () => { const t = M().track("file:" + j.out) || (M().lib().songs || []).find(x => x.u === j.out); if (t) M().playList([t]); });
        if (j.st === "DONE" && kind === "video") btn("Watch", "play", () => { if (window.BlazeVideo) { const t = (M().lib().videos || []).find(x => x.u === j.out); if (t) BlazeVideo.play(t); } });
        btn(act || j.st === "PAUSED" ? "Cancel" : "Remove from list", "x", () => (act || j.st === "PAUSED" ? API().MediaDlCancel(j.id) : API().MediaDlDismiss(j.id)));
        box.append(r);
      });
    };
    window.addEventListener("blazemedia:downloads", draw);
    draw();
  }
  return { view, downloads, open };
})();
window.BlazeMediaStore = BlazeMediaStore;
