// v0.19 Blaze Video (ported from Blaze Mobile's VideoActivity / VideoUi). Library: the user's
// Videos folder and Blaze downloads, with Continue watching (resume position), series grouped
// from titles (S01E02, 1x02, Episode 3), the Store and downloads. Player: speed, subtitles
// (files next to the video, saved tracks, YouTube captions, load a .srt/.vtt, AI translation),
// Skip intro (learned per series from the first jump past it), Up next countdown with autoplay,
// AI recap of what happened so far, picture-in-picture and full screen. The agent drives it
// through video_ops ("media:cmd" video / subs / subs_off).
const BlazeVideo = (() => {
  const L = window.BlazeMediaLogic, M = () => window.BlazeMusic;
  const mi = (n, c) => M().mi(n, c);
  const VI = {
    back10: '<path d="M3 8a5 5 0 1 0 1.5-3.5"/><path d="M4.5 2v2.5H7"/><text x="8" y="10" font-size="4.6" text-anchor="middle" fill="currentColor" stroke="none" font-family="sans-serif">10</text>',
    fwd10: '<path d="M13 8a5 5 0 1 1-1.5-3.5"/><path d="M11.5 2v2.5H9"/><text x="8" y="10" font-size="4.6" text-anchor="middle" fill="currentColor" stroke="none" font-family="sans-serif">10</text>',
    cc: '<rect x="1.5" y="3.5" width="13" height="9" rx="2"/><path d="M7 6.8a1.8 1.8 0 1 0 0 2.4M11.5 6.8a1.8 1.8 0 1 0 0 2.4"/>',
    full: '<path d="M2.5 6V2.5H6M10 2.5h3.5V6M13.5 10v3.5H10M6 13.5H2.5V10"/>', pip: '<rect x="1.5" y="3" width="13" height="10" rx="1.5"/><rect x="8" y="8" width="5" height="3.5" rx=".8" fill="currentColor"/>',
    recap: '<path d="M3 3.5h10v7H7l-3 2.5v-2.5H3z"/><path d="M5.5 6h5M5.5 8h3"/>', globe: '<circle cx="8" cy="8" r="6"/><path d="M2 8h12M8 2c2 2 2 10 0 12M8 2c-2 2-2 10 0 12"/>',
    skip: '<path d="M3 3.5v9l6-4.5z" fill="currentColor"/><path d="M11.5 3.5v9"/>',
  };
  const vi = (n, cls = "i") => (VI[n] ? `<svg class="${cls}" viewBox="0 0 16 16">${VI[n]}</svg>` : mi(n, cls));

  let tab = "library", q = "", openSeries = null, root = null;
  const V = { el: null, track: null, subs: [], subOn: "", speed: 1, intro: 0, series: "", upNext: null, countFrom: 0, lastSave: 0, lastReport: 0, jumpFrom: -1, hideT: 0 };
  const lib = () => M().lib();
  const pos = k => (lib().positions || {})[k] || 0;

  // ---------------------------------------------------------------- the view
  function videoView(el, arg) {
    if (arg && ["library", "store", "downloads"].includes(arg)) tab = arg;
    root = h("div", "mz mzvideo"); el.append(root);
    M().init().then(paint);
    paint();
  }
  function paint() {
    if (!root || !root.isConnected) return;
    if (V.track && V.el && root.contains(V.el)) return; // never rebuild under a playing video
    root.innerHTML = `
      <div class="mzhead">
        <div class="mzh-t"><span class="label acc">Blaze Video</span><h1>${tab === "store" ? "Find videos" : tab === "downloads" ? "Downloads" : "Your videos"}</h1></div>
        <div class="seg mzpill">${[["library", "Library"], ["store", "Store"], ["downloads", "Downloads"]].map(([id, n]) => `<button data-t="${id}" class="${tab === id ? "on" : ""}">${n}</button>`).join("")}</div>
        <span class="grow"></span><button class="btn ghost sm mzswitch">${mi("note", "i sm")}Music</button>
      </div>
      <div class="mzvplayer" hidden></div><div class="mzmain"></div>`;
    $$(".mzpill button", root).forEach(b => b.onclick = () => { tab = b.dataset.t; paint(); });
    $(".mzswitch", root).onclick = () => show("music");
    const main = $(".mzmain", root);
    if (tab === "store") BlazeMediaStore.view(main, "video", { play: playStream });
    else if (tab === "downloads") BlazeMediaStore.downloads(main, "video");
    else library(main);
  }
  function library(main) {
    const vids = lib().videos || [];
    main.innerHTML = `<div class="mzbar"><label class="mzsearch">${mi("search", "i sm")}<input placeholder="Search your videos" value="${esc(q)}"></label></div><div class="mzlist"></div>`;
    const inp = $("input", main), box = $(".mzlist", main);
    inp.oninput = () => { q = inp.value; draw(); };
    const draw = () => {
      box.innerHTML = "";
      if (!vids.length) {
        box.append(h("div", "mzempty big", `<span class="mzemblem"><blaze-emblem size="44" ${lib().scanning ? "live" : ""}></blaze-emblem></span><b>${lib().scanning ? "Looking through your videos\u2026" : "No videos yet"}</b>Videos in your Videos folder show up here, with subtitles and where you left off. Find more in the Store.`));
        const a = h("div", "mzacts center", `<button class="btn accent sm">${mi("search", "i sm")}Open the Store</button>`); $("button", a).onclick = () => { tab = "store"; paint(); }; box.append(a);
        return;
      }
      if (q) { const f = L.search(vids, q); grid(box, f); if (!f.length) box.append(h("div", "mzempty", `<b>No match</b>Nothing matches \u201c${esc(q)}\u201d.`)); return; }
      const series = seriesOf(vids);
      if (openSeries) {
        const s = series.find(x => x.name === openSeries);
        if (s) { const back = h("button", "btn ghost sm", `${mi("prev", "i sm")}All videos`); back.onclick = () => { openSeries = null; draw(); }; const hd = h("div", "mzlh", `<span class="t">${esc(s.name)} \u00b7 ${s.list.length} episodes</span><span class="grow"></span>`); hd.append(back); box.append(hd); grid(box, s.list); return; }
      }
      const cont = vids.filter(v => L.resumeAt(pos(v.k), v.d) > 0).sort((a, b) => (lib().recent_videos || []).indexOf(a.k) - (lib().recent_videos || []).indexOf(b.k));
      if (cont.length) { box.append(h("div", "mzlh", '<span class="t">Continue watching</span>')); grid(box, cont.slice(0, 8), true); }
      if (series.length) {
        box.append(h("div", "mzlh", '<span class="t">Series</span>'));
        const g = h("div", "mzvgrid");
        series.forEach(s => { const c = h("button", "mzitem", `<span class="th">${thumb(s.list[0])}<span class="dur">${s.list.length} ep</span></span><span class="t">${esc(s.name)}</span><span class="s">${esc(s.list[0].t)}</span>`); c.onclick = () => { openSeries = s.name; draw(); }; g.append(c); });
        box.append(g);
      }
      box.append(h("div", "mzlh", `<span class="t">All videos \u00b7 ${vids.length}</span>`));
      grid(box, vids);
    };
    draw();
  }
  const thumb = v => { const a = M().artURL(v); return a ? `<img src="${esc(a)}" alt="" loading="lazy" onerror="this.remove()">` : `<span class="ph">${mi("film")}</span>`; };
  function seriesOf(vids) {
    const m = new Map();
    vids.forEach(v => { const e = L.episode(v.t); if (!e) return; const k = e.series.toLowerCase(); if (!m.has(k)) m.set(k, { name: e.series, list: [] }); m.get(k).list.push(Object.assign({ _e: e }, v)); });
    return [...m.values()].filter(s => s.list.length >= 2).map(s => (s.list.sort((a, b) => a._e.season - b._e.season || a._e.episode - b._e.episode), s));
  }
  function grid(box, list, progressBar) {
    const g = h("div", "mzvgrid");
    list.forEach(v => {
      const p = pos(v.k), pct = v.d ? Math.min(100, p / v.d * 100) : 0;
      const c = h("button", "mzitem" + (V.track && V.track.k === v.k ? " on" : ""), `<span class="th">${thumb(v)}${v.d ? `<span class="dur">${L.clock(v.d)}</span>` : ""}${(progressBar || p) && pct ? `<span class="pbar"><i style="width:${pct}%"></i></span>` : ""}</span><span class="t">${esc(v.t)}</span><span class="s">${esc([v.a, v.src === "download" ? "Downloaded" : ""].filter(Boolean).join(" \u00b7 "))}</span>`);
      c.onclick = () => play(v);
      c.oncontextmenu = e => { e.preventDefault(); M().menu(c, [["Play", () => play(v)], ["Rename\u2026", () => rename(v)], [v.src === "download" ? "Delete from this PC" : "Delete file\u2026", () => del(v), "warn"]]); };
      g.append(c);
    });
    box.append(g);
  }
  function rename(v) {
    const f = h("input", "field mzfield"); f.value = v.t;
    M().dialog("Rename", f, [["Cancel", () => {}, "ghost"], ["Save", () => { API().MediaRename(v.k, f.value, v.a || ""); }, "accent"]]);
  }
  function del(v) {
    const own = v.src !== "download";
    M().dialog(`Delete \u201c${v.t.slice(0, 40)}\u201d?`, own ? "It's in your own folder. Blaze removes the file from this PC." : "It's removed from this PC.", [["Cancel", () => {}, "ghost"], ["Delete", () => { if (V.track && V.track.k === v.k) close(); API().MediaDelete(v.k, own).then(e => e && M().toast(e)); }, "accent warn"]]);
  }

  // ---------------------------------------------------------------- the player
  async function play(v, at) {
    if (view !== "video") show("video");
    await M().init();
    M().pause();
    const box = root && $(".mzvplayer", root); if (!box) return;
    if (V.track) save(true);
    V.track = v; V.speed = 1; V.subOn = ""; V.upNext = null; V.countFrom = 0; V.jumpFrom = -1;
    const e = L.episode(v.t); V.series = e ? e.series.toLowerCase() : "";
    V.intro = V.series ? ((lib().prefs || {}).intros || {})[V.series] || 0 : 0;
    box.hidden = false;
    box.innerHTML = `
      <div class="mzv glass">
        <div class="mzv-stage"><video playsinline preload="metadata"></video>
          <button class="mzv-skip" hidden>${vi("skip", "i sm")}Skip intro</button>
          <div class="mzv-next" hidden></div><div class="mzv-recap" hidden></div>
          <div class="mzv-ctl">
            <input class="mzv-seek" type="range" min="0" max="1000" value="0" aria-label="Seek">
            <div class="mzv-row">
              <button class="mzround pp" aria-label="Play">${mi("play", "i sm")}</button>
              <button class="mzround ghost b10" aria-label="Back 10 seconds">${vi("back10")}</button><button class="mzround ghost f10" aria-label="Forward 10 seconds">${vi("fwd10")}</button>
              <span class="tm">0:00 / ${L.clock(v.d)}</span><span class="grow"></span>
              <button class="mzchip dark sp" title="Speed">1\u00d7</button>
              <button class="mzround ghost cc" aria-label="Subtitles">${vi("cc")}</button>
              <button class="mzround ghost more" aria-label="More">${mi("more")}</button>
              ${document.pictureInPictureEnabled ? `<button class="mzround ghost pip" aria-label="Picture in picture">${vi("pip")}</button>` : ""}
              <button class="mzround ghost fs" aria-label="Full screen">${vi("full")}</button>
            </div>
          </div>
        </div>
        <div class="mzv-meta"><div class="m"><div class="t">${esc(v.t)}</div><div class="s">${esc([v.a, e ? `Season ${e.season} \u00b7 Episode ${e.episode}` : ""].filter(Boolean).join(" \u00b7 "))}</div></div>
          <button class="btn ghost sm rc">${vi("recap", "i sm")}Recap</button><button class="mzround ghost x" aria-label="Close player">${mi("x")}</button></div>
      </div>`;
    const el = V.el = $("video", box);
    if (M().info().base) el.crossOrigin = "anonymous";
    el.src = v.stream ? M().url(v.stream) : M().fileURL(v);
    const start = at != null ? at : L.resumeAt(pos(v.k), v.d);
    el.addEventListener("loadedmetadata", () => { if (start) el.currentTime = start / 1000; if (!v.d && isFinite(el.duration) && !v.stream) API().MediaDuration(v.k, Math.round(el.duration * 1000)); });
    el.addEventListener("timeupdate", tick);
    el.addEventListener("play", controls); el.addEventListener("pause", () => { controls(); save(true); report(true); });
    el.addEventListener("ended", ended);
    el.addEventListener("seeking", () => { if (V.jumpFrom < 0) V.jumpFrom = V.lastPos || 0; });
    el.addEventListener("seeked", () => { learnIntro(V.jumpFrom, el.currentTime * 1000); V.jumpFrom = -1; });
    el.addEventListener("error", () => M().toast("Couldn't play this video. This PC may not decode its format (try an mp4 / H.264 copy).", 5000));
    el.addEventListener("click", () => toggle());
    el.addEventListener("dblclick", () => full());
    wire(box);
    el.play().catch(() => {});
    if (!v.stream) API().MediaPlayed(v.k);
    loadSubs(v);
    session(v);
    box.scrollIntoView({ block: "start", behavior: "smooth" });
  }
  function wire(box) {
    const el = V.el;
    $(".pp", box).onclick = toggle;
    $(".b10", box).onclick = () => { el.currentTime = Math.max(0, el.currentTime - 10); };
    $(".f10", box).onclick = () => { el.currentTime = Math.min(el.duration || 0, el.currentTime + 10); };
    const sk = $(".mzv-seek", box);
    sk.oninput = () => { sk.dataset.drag = "1"; };
    sk.onchange = () => { delete sk.dataset.drag; if (isFinite(el.duration)) el.currentTime = sk.value / 1000 * el.duration; };
    $(".sp", box).onclick = e => { V.speed = L.nextSpeed(V.speed); el.playbackRate = V.speed; e.currentTarget.textContent = L.speedLabel(V.speed); };
    $(".cc", box).onclick = e => { e.stopPropagation(); subsMenu(e.currentTarget); };
    $(".more", box).onclick = e => { e.stopPropagation(); moreMenu(e.currentTarget); };
    const pip = $(".pip", box); if (pip) pip.onclick = () => (document.pictureInPictureElement ? document.exitPictureInPicture() : el.requestPictureInPicture()).catch(() => {});
    $(".fs", box).onclick = full;
    $(".rc", box).onclick = recap;
    $(".x", box).onclick = close;
    $(".mzv-skip", box).onclick = () => { if (V.intro) el.currentTime = V.intro / 1000; };
    const stage = $(".mzv-stage", box);
    stage.onmousemove = () => { stage.classList.add("show"); clearTimeout(V.hideT); V.hideT = setTimeout(() => { if (!el.paused) stage.classList.remove("show"); }, 2600); };
    stage.classList.add("show");
  }
  function full() { const s = V.el && V.el.closest(".mzv-stage"); if (!s) return; document.fullscreenElement ? document.exitFullscreen() : (s.requestFullscreen ? s.requestFullscreen() : V.el.webkitEnterFullscreen && V.el.webkitEnterFullscreen()); }
  function toggle() { const el = V.el; if (!el) return; if (el.paused) { M().pause(); el.play().catch(() => {}); } else el.pause(); }
  function controls() {
    const box = V.el && V.el.closest(".mzvplayer"); if (!box) return;
    const p = $(".pp", box); p.innerHTML = mi(V.el.paused ? "play" : "pause", "i sm"); p.setAttribute("aria-label", V.el.paused ? "Play" : "Pause");
    $(".mzv-stage", box).classList.toggle("paused", V.el.paused);
    if (V.el.paused) $(".mzv-stage", box).classList.add("show");
  }
  function tick() {
    const el = V.el; if (!el || !V.track) return;
    const p = el.currentTime * 1000, d = isFinite(el.duration) ? el.duration * 1000 : V.track.d || 0;
    if (!el.seeking) V.lastPos = p;
    const box = el.closest(".mzvplayer"); if (!box) return;
    const sk = $(".mzv-seek", box);
    if (!sk.dataset.drag) { sk.value = d ? Math.round(p / d * 1000) : 0; sk.style.setProperty("--p", (d ? p / d * 100 : 0) + "%"); }
    $(".tm", box).textContent = `${L.clock(p)} / ${L.clock(d)}`;
    $(".mzv-skip", box).hidden = !L.showSkip(p, V.intro);
    if (Date.now() - V.lastSave > 5000) save();
    report();
    if (d && d - p < 1000 * 12 && !V.upNext && (lib().prefs || {}).autoplay !== false) nextUp();
  }
  async function nextUp() {
    V.upNext = false;
    const n = V.track && !V.track.stream ? await API().MediaNextEpisode(V.track.k) : null;
    if (!n) return;
    V.upNext = n;
    const box = V.el.closest(".mzvplayer"), card = $(".mzv-next", box);
    card.hidden = false;
    card.innerHTML = `<span class="label">Up next</span><span class="t">${esc(n.t)}</span><span class="a"><button class="btn accent xs go">Play now</button><button class="btn ghost xs no">Cancel</button></span>`;
    $(".go", card).onclick = () => play(n);
    $(".no", card).onclick = () => { V.upNext = false; card.hidden = true; };
  }
  function ended() {
    save(true);
    const n = V.upNext;
    if (!n) return;
    const box = V.el.closest(".mzvplayer"), card = $(".mzv-next", box);
    V.countFrom = Date.now();
    const step = () => {
      if (V.upNext !== n || !card.isConnected) return;
      const left = L.countdown(V.countFrom, Date.now(), 5);
      if (left < 0) { play(n); return; }
      const t = $(".t", card); if (t) t.textContent = `${n.t} \u00b7 in ${left}`;
      setTimeout(step, 250);
    };
    step();
  }
  function save(force) {
    const el = V.el, t = V.track; if (!el || !t || t.stream) return;
    V.lastSave = Date.now();
    const p = Math.round(el.currentTime * 1000), d = isFinite(el.duration) ? Math.round(el.duration * 1000) : t.d || 0;
    if (!force && p < 5000) return;
    API().MediaSavePosition(t.k, p, d);
    lib().positions = lib().positions || {};
    if (L.resumeAt(p, d)) lib().positions[t.k] = p; else delete lib().positions[t.k];
  }
  function report(force) {
    if (!force && Date.now() - V.lastReport < 3000) return;
    V.lastReport = Date.now();
    const el = V.el, t = V.track; if (!t) return;
    try { API().MediaReport({ kind: "video", key: t.stream ? "" : t.k, title: t.t, artist: t.a || "", playing: !!(el && !el.paused), pos_ms: el ? Math.round(el.currentTime * 1000) : 0, dur_ms: el && isFinite(el.duration) ? Math.round(el.duration * 1000) : t.d || 0, index: 0, count: 1, shuffle: false, repeat: "off", volume: 1, sub_id: V.subOn, up_next: [] }); } catch (e) {}
  }
  async function learnIntro(from, to) {
    if (!V.series || from < 0) return;
    if (from <= 180000 && to - from >= 20000 && to - from <= 150000 && to <= 300000) { V.intro = await API().MediaIntro(V.series, from, to); }
  }
  function close() {
    save(true);
    if (V.el) { V.el.pause(); V.el.removeAttribute("src"); V.el.load(); }
    V.el = null; V.track = null; V.upNext = null;
    try { API().MediaReport({ kind: "", key: "" }); } catch (e) {}
    const box = root && $(".mzvplayer", root); if (box) { box.hidden = true; box.innerHTML = ""; }
    if (root && root.isConnected && tab === "library") library($(".mzmain", root));
  }
  function session(v) {
    const ms = navigator.mediaSession; if (!ms) return;
    try { ms.metadata = new MediaMetadata({ title: v.t, artist: v.a || "Blaze Video", artwork: [] }); ms.setActionHandler("play", toggle); ms.setActionHandler("pause", toggle); } catch (e) {}
  }

  // ---------------------------------------------------------------- subtitles
  async function loadSubs(v) {
    V.subs = [];
    if (v.stream) return;
    try { V.subs = await API().MediaVideoSubs(v.k, false) || []; } catch (e) {}
    const pref = (lib().prefs || {}).subs_pref || "";
    if (pref && pref !== "off") {
      const pick = V.subs.find(s => s.lang && s.lang.split(/[-_]/)[0] === pref && !s.auto) || V.subs.find(s => s.lang && s.lang.split(/[-_]/)[0] === pref);
      if (pick) subOn(pick);
    }
  }
  function subOn(s) {
    const el = V.el; if (!el || !V.track) return;
    [...el.querySelectorAll("track")].forEach(t => t.remove());
    V.subOn = "";
    if (!s) { report(true); return; }
    const tr = document.createElement("track");
    tr.kind = "subtitles"; tr.label = s.label; tr.srclang = (s.lang || "und").slice(0, 8); tr.default = true;
    tr.src = M().url("/media/sub?v=" + encodeURIComponent(V.track.k) + "&id=" + encodeURIComponent(s.id) + "&r=" + Date.now());
    el.append(tr);
    tr.addEventListener("load", () => { if (el.textTracks[0]) el.textTracks[0].mode = "showing"; });
    setTimeout(() => { [...el.textTracks].forEach(t => t.mode = "showing"); }, 300);
    V.subOn = s.id;
    report(true);
  }
  async function subsMenu(anchor) {
    const v = V.track; if (!v) return;
    let online = [];
    openPop(anchor, p => {
      p.classList.add("mzmenu");
      const draw = () => {
        p.innerHTML = '<div class="phd"><span class="label">Subtitles</span></div>';
        const add = (label, fn, on) => { const b = h("button", "mi" + (on ? " on" : ""), esc(label) + (on ? "  \u2713" : "")); b.onclick = e => { e.stopPropagation(); fn(); }; p.append(b); };
        add("Off", () => { subOn(null); API().MediaSetPref("subs_pref", "off"); closePop(); }, !V.subOn);
        [...V.subs, ...online.filter(o => !V.subs.some(s => s.id === o.id))].forEach(s => add(s.label, async () => {
          closePop();
          let use = s;
          if (!s.loaded) { M().toast("Getting " + s.label + " subtitles\u2026"); const r = await API().MediaLoadSub(v.k, s.id); if (r.error) { M().toast(r.error, 4000); return; } use = r.sub; V.subs.push(use); }
          subOn(use); if (use.lang) API().MediaSetPref("subs_pref", use.lang.split(/[-_]/)[0]);
        }, V.subOn === s.id));
        if (v.vid && v.vid.startsWith("yt:") && !online.length) add("YouTube captions\u2026", async () => { p.append(h("div", "mzsmall", "Looking\u2026")); online = (await API().MediaVideoSubs(v.k, true) || []).filter(s => !s.loaded); if (!online.length) M().toast("No captions on YouTube for this video."); draw(); });
        add("Load a subtitle file\u2026", async () => { closePop(); const r = await API().MediaPickSubFile(v.k); if (r.error) M().toast(r.error, 4000); else if (r.sub && r.sub.id) { V.subs.push(r.sub); subOn(r.sub); } });
        add("Translate with AI\u2026", () => { closePop(); translate(); });
      };
      draw();
    }, { width: 260 });
  }
  function translate() {
    const v = V.track; if (!v) return;
    const f = h("input", "field mzfield"); f.placeholder = "Language, e.g. French, Bemba, Portuguese";
    M().dialog("Translate subtitles", f, [["Cancel", () => {}, "ghost"], ["Translate", () => {
      const lang = f.value.trim(); if (!lang) return false;
      M().toast("Translating subtitles to " + lang + "\u2026", 3000);
      API().MediaTranslate(v.k, lang, V.subOn).then(r => { if (r.error) { M().toast(r.error, 5000); return; } V.subs.push(r.sub); if (V.track === v) subOn(r.sub); M().toast(r.note || "Translated", 3500); });
    }, "accent"]]);
    setTimeout(() => f.focus(), 50);
  }
  async function recap() {
    const v = V.track, el = V.el; if (!v || !el) return;
    const box = el.closest(".mzvplayer"), card = $(".mzv-recap", box);
    card.hidden = false; card.innerHTML = `<div class="label">Recap</div><div class="mzsmall"><blaze-emblem size="18" live></blaze-emblem> Reading what's been said so far\u2026</div>`;
    const text = await API().MediaRecap(v.k, Math.round(el.currentTime * 1000), V.subOn);
    if (!card.isConnected) return;
    card.innerHTML = `<div class="mzv-rh"><span class="label">Recap so far</span><span class="grow"></span><button class="mzround ghost sm" aria-label="Close recap">${mi("x", "i sm")}</button></div><div class="body">${MD && MD.render ? MD.render(text) : esc(text)}</div>`;
    $("button", card).onclick = () => { card.hidden = true; };
  }
  function moreMenu(anchor) {
    const p = lib().prefs || {};
    M().menu(anchor, [
      ["Recap so far", recap],
      ["Translate subtitles\u2026", translate],
      V.series && [V.intro ? "Intro ends here (update)" : "Mark the end of the intro here", async () => { V.intro = await API().MediaIntro(V.series, -1, Math.round(V.el.currentTime * 1000)); M().toast("Skip intro is set for this series"); }],
      V.series && V.intro && ["Forget the intro", async () => { V.intro = await API().MediaIntro(V.series, -1, 0); }],
      [`Autoplay next episode: ${p.autoplay === false ? "off" : "on"}`, async () => { lib().prefs = await API().MediaSetPref("autoplay", String(p.autoplay === false)); }],
      [`Smart downloads: ${p.smart_downloads ? "on" : "off"}`, async () => { lib().prefs = await API().MediaSetPref("smart_downloads", String(!p.smart_downloads)); M().toast(p.smart_downloads ? "Smart downloads off" : "Watched downloaded episodes are cleared a day after you finish them"); }],
    ]);
  }
  function playStream(item, v) {
    if (!v.stream) { M().toast("This one can't play right away. Download it instead."); return; }
    play({ k: "stream:" + item.source + ":" + item.id, t: v.detail.title || item.title, a: v.detail.author || item.author, art: item.thumb, d: v.stream_ms, stream: v.stream, kind: "video" }, 0);
  }

  // The agent: video_ops play / subtitles.
  async function cmd(c) {
    await M().init();
    if (c.op === "video") {
      let t = (lib().videos || []).find(v => v.k === (c.keys || [])[0]);
      if (!t) { await M().reload(); t = (lib().videos || []).find(v => v.k === (c.keys || [])[0]); }
      if (t) play(t);
    } else if (c.op === "subs") {
      if (!V.track) return;
      V.subs = await API().MediaVideoSubs(V.track.k, false) || V.subs;
      const s = V.subs.find(x => x.id === c.value); if (s) subOn(s);
    } else if (c.op === "subs_off") subOn(null);
  }
  window.addEventListener("blazemedia:library", () => { if (view === "video" && !V.track) paint(); });
  const vw = document.getElementById("view");
  if (vw) new MutationObserver(() => { if (V.el && !V.el.isConnected) { save(true); V.el = null; V.track = null; } }).observe(vw, { childList: true });

  window.VIEWS = window.VIEWS || {};
  window.VIEWS.video = (el, arg) => videoView(el, arg);
  if (window.BlazeApps) BlazeApps.register({ id: "video", name: "Video", icon: '<rect x="2" y="3" width="12" height="10" rx="2"/><path d="m6.5 6 3.5 2-3.5 2z"/>', desc: "Your videos, series and subtitles", open: () => show("video") });
  return { cmd, play, pause: () => V.el && V.el.pause(), active: () => !!(V.el && V.el.isConnected && !V.el.paused), paint };
})();
window.BlazeVideo = BlazeVideo;
