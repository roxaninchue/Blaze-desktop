// v0.19 Blaze Music (ported from Blaze Mobile's MusicActivity / MusicService / MediaUi). The
// library is the user's music folders plus Blaze's downloads (internal/media, media_app.go);
// one <audio> lives outside the views, so music keeps playing on every tab, with a mini player
// in the corner. Player: queue, shuffle, repeat, sleep timer (fade out), equalizer + fades
// (WebAudio), lyrics and karaoke (LRCLIB), likes, playlists, smart lists, daily mixes, Blaze DJ,
// Wrapped, the Store (YouTube, Jamendo, Internet Archive, by link) and downloads. The agent
// drives it through "media:cmd" events (music_play / music_ops). OS media keys go through
// navigator.mediaSession where the webview supports it.
//
// Entry points (kept to a tiny hook so the shell rebuild can move them): BlazeViews.LIST gets
// Music and Video, window.VIEWS.music renders the view, window.BlazeApps.list is a registry a
// Home screen can read, and a small round button pair sits in the top bar.
const BlazeMusic = (() => {
  const L = window.BlazeMediaLogic;
  // ---------------------------------------------------------------- the apps registry (Home hook)
  const Apps = window.BlazeApps = window.BlazeApps || {
    list: [], register(a) { if (!this.list.some(x => x.id === a.id)) this.list.push(a); window.dispatchEvent(new CustomEvent("blazeapps")); },
  };

  // ---------------------------------------------------------------- icons (16 px grid, round caps)
  const MI = {
    note: '<path d="M6 12.5V3.5l7-1.5v9"/><circle cx="4.5" cy="12.5" r="1.8"/><circle cx="11.5" cy="11" r="1.8"/>',
    film: '<rect x="2" y="3" width="12" height="10" rx="2"/><path d="m6.5 6 3.5 2-3.5 2z"/>',
    play: '<path d="M5 3.2v9.6c0 .4.4.6.7.4l7.4-4.8a.5.5 0 0 0 0-.8L5.7 2.8c-.3-.2-.7 0-.7.4z" fill="currentColor"/>',
    pause: '<rect x="4" y="3" width="2.6" height="10" rx="1" fill="currentColor"/><rect x="9.4" y="3" width="2.6" height="10" rx="1" fill="currentColor"/>',
    next: '<path d="M4 3.5v9l6.5-4.5z" fill="currentColor"/><path d="M12.5 3.5v9"/>', prev: '<path d="M12 3.5v9L5.5 8z" fill="currentColor"/><path d="M3.5 3.5v9"/>',
    shuffle: '<path d="M2 4.5h2.5c3 0 4 7 7 7H14M2 11.5h2.5c1.2 0 2-.9 2.7-2M9.3 6.5c.7-1.2 1.5-2 2.7-2H14M12 2.5l2 2-2 2M12 9.5l2 2-2 2"/>',
    repeat: '<path d="M3 7V6a2 2 0 0 1 2-2h8M11 2l2 2-2 2M13 9v1a2 2 0 0 1-2 2H3M5 14l-2-2 2-2"/>',
    heart: '<path d="M8 13.5s-5.5-3.2-5.5-7A3 3 0 0 1 8 4.8a3 3 0 0 1 5.5 1.7c0 3.8-5.5 7-5.5 7z"/>',
    heartf: '<path d="M8 13.5s-5.5-3.2-5.5-7A3 3 0 0 1 8 4.8a3 3 0 0 1 5.5 1.7c0 3.8-5.5 7-5.5 7z" fill="currentColor"/>',
    queue: '<path d="M2.5 4h8M2.5 8h8M2.5 12h5M12.5 10v4M10.5 12h4"/>', lyrics: '<path d="M3 4.5h10M3 8h7M3 11.5h8"/>',
    mic: '<rect x="6" y="2" width="4" height="7" rx="2"/><path d="M4 7.5a4 4 0 0 0 8 0M8 11.5V14"/>',
    sleep: '<path d="M13 9.5A5.5 5.5 0 0 1 6.5 3a5.5 5.5 0 1 0 6.5 6.5z"/>', eq: '<path d="M3 13V8M3 5V3M8 13v-2M8 8V3M13 13V9M13 6V3M1.5 6.5h3M6.5 9.5h3M11.5 7.5h3"/>',
    fade: '<path d="M2 12c3 0 3-8 6-8s3 8 6 8"/>', search: '<circle cx="7" cy="7" r="4.5"/><path d="m10.5 10.5 3.5 3.5"/>',
    folder: '<path d="M1.5 4.5a1 1 0 0 1 1-1h3.5l1.5 1.5h6a1 1 0 0 1 1 1v6.5a1 1 0 0 1-1 1h-11a1 1 0 0 1-1-1z"/>',
    more: '<circle cx="3.5" cy="8" r=".9" fill="currentColor"/><circle cx="8" cy="8" r=".9" fill="currentColor"/><circle cx="12.5" cy="8" r=".9" fill="currentColor"/>',
    dl: '<path d="M8 2v8.5M4.5 7 8 10.5 11.5 7M2.5 13.5h11"/>', x: '<path d="m4 4 8 8M12 4l-8 8"/>', plus: '<path d="M8 3v10M3 8h10"/>',
    retry: '<path d="M13 3.5v3h-3"/><path d="M12.6 6.5A5 5 0 1 0 13 9"/>', link: '<path d="M6.5 9.5 9.5 6.5M7 4.5l1-1a2.8 2.8 0 0 1 4 4l-1 1M9 11.5l-1 1a2.8 2.8 0 0 1-4-4l1-1"/>',
    dj: '<circle cx="8" cy="8" r="5.5"/><circle cx="8" cy="8" r="1.5"/><path d="M8 2.5v2"/>', spark: '<path d="M8 2v3M8 11v3M2 8h3M11 8h3M4 4l2 2M10 10l2 2M4 12l2-2M10 6l2-2"/>',
    vol: '<path d="M2.5 6h2.5L8.5 3v10L5 10H2.5z"/><path d="M11 5.5a3.5 3.5 0 0 1 0 5"/>',
  };
  const mi = (n, cls = "i") => `<svg class="${cls}" viewBox="0 0 16 16">${MI[n] || ""}</svg>`;
  if (typeof IC !== "undefined") { IC.music = MI.note; IC.film = MI.film; }

  // ---------------------------------------------------------------- service + library
  let info = { base: "", token: "", eq: ["Flat"], sleep: [5, 10, 15, 30, 45, 60, 90] };
  let lib = { songs: [], videos: [], playlists: [], liked: [], recent: [], recent_videos: [], positions: {}, folders: [], prefs: {} };
  let byKey = new Map(), ready = null;
  const url = p => (info.base ? info.base + p + (p.includes("?") ? "&" : "?") + "t=" + info.token : p);
  const fileURL = t => url("/media/file?k=" + encodeURIComponent(t.k));
  const artURL = t => (!t || !t.art ? "" : /^(https?:|data:)/.test(t.art) ? t.art : url("/media/art?k=" + encodeURIComponent(t.k)));
  const toast = (text, ms = 2600) => {
    document.querySelectorAll(".mztoast").forEach(t => t.remove());
    const t = h("div", "mztoast", esc(text)); document.body.append(t);
    requestAnimationFrame(() => t.classList.add("in"));
    setTimeout(() => { t.classList.remove("in"); setTimeout(() => t.remove(), 260); }, ms);
  };
  function init() {
    if (!ready) ready = (async () => {
      try { info = Object.assign(info, await API().MediaInit() || {}); } catch (e) {}
      await reload();
    })();
    return ready;
  }
  let reloadT = 0;
  async function reload() {
    try { lib = Object.assign(lib, await API().MediaLibrary() || {}); } catch (e) {}
    byKey = new Map([...(lib.songs || []), ...(lib.videos || [])].map(t => [t.k, t]));
    window.dispatchEvent(new CustomEvent("blazemedia:library"));
    if (view === "music") paint();
  }
  const soon = () => { clearTimeout(reloadT); reloadT = setTimeout(reload, 250); };
  const track = k => byKey.get(k);
  const liked = k => (lib.liked || []).includes(k);

  // ---------------------------------------------------------------- the player (one <audio>, every tab)
  const P = {
    audio: null, ctx: null, gain: null, filters: [], queue: [], idx: -1, order: [], shuffle: false, repeat: "off",
    volume: 1, sleepAt: 0, playFrom: 0, playKey: "", lastReport: 0, error: "", eq: "Flat", fade: 0, sleepFade: true, dj: false,
  };
  function audio() {
    if (P.audio) return P.audio;
    const a = P.audio = new Audio();
    a.preload = "auto";
    if (info.base) a.crossOrigin = "anonymous";
    a.addEventListener("timeupdate", tick);
    a.addEventListener("play", () => { startPlay(); sync(); });
    a.addEventListener("pause", () => { flushPlay(); sync(); report(true); });
    a.addEventListener("ended", () => { flushPlay(); next(true); });
    a.addEventListener("loadedmetadata", () => { const t = cur(); if (t && a.duration && isFinite(a.duration)) { if (!t.d) API().MediaDuration(t.k, Math.round(a.duration * 1000)); t.d = Math.round(a.duration * 1000); } sync(); });
    a.addEventListener("error", () => { P.error = "Couldn't play this file. It may have been moved or deleted, or this PC can't decode its format."; sync(); });
    return a;
  }
  // WebAudio (equalizer, fades) once the user has played something; plain volume before that.
  function graph() {
    if (P.ctx || !window.AudioContext) return;
    try {
      const ctx = new AudioContext(), src = ctx.createMediaElementSource(audio());
      P.filters = (info.bands || [60, 230, 910, 3600, 14000]).map((f, i, arr) => {
        const b = ctx.createBiquadFilter(); b.type = i === 0 ? "lowshelf" : i === arr.length - 1 ? "highshelf" : "peaking"; b.frequency.value = f; b.Q.value = 1.1; return b;
      });
      P.gain = ctx.createGain();
      [src, ...P.filters, P.gain].reduce((a, b) => (a.connect(b), b)).connect(ctx.destination);
      P.ctx = ctx;
      applyEQ(P.eq);
    } catch (e) { P.ctx = null; }
  }
  async function applyEQ(name) {
    P.eq = name || "Flat";
    if (!P.filters.length) return;
    let gains = [];
    try { gains = await API().MediaEQ(P.eq) || []; } catch (e) {}
    P.filters.forEach((f, i) => f.gain.value = gains[i] || 0);
  }
  const cur = () => P.queue[P.idx] || null;
  function startPlay() { flushPlay(); const t = cur(); if (t) { P.playKey = t.k; P.playFrom = Date.now(); } }
  function flushPlay() {
    if (P.playKey && P.playFrom) { const ms = Date.now() - P.playFrom; if (ms >= 3000 && !P.playKey.startsWith("stream:")) API().MediaLogPlay(P.playKey, P.playFrom, ms); }
    P.playKey = ""; P.playFrom = 0;
  }
  function srcOf(t) { return t.stream ? url(t.stream) : fileURL(t); }
  function load(i, play = true, at = 0) {
    const t = P.queue[i]; if (!t) return;
    P.idx = i; P.error = "";
    const a = audio();
    a.src = srcOf(t);
    if (at) a.currentTime = at / 1000;
    if (play) { graph(); if (P.ctx && P.ctx.state === "suspended") P.ctx.resume(); a.play().catch(() => {}); }
    if (!t.stream) API().MediaPlayed(t.k);
    if (window.BlazeVideo && play) BlazeVideo.pause();
    session(); sync(); report(true);
  }
  function playList(list, start = 0, shuffle = false) {
    if (!list || !list.length) return;
    P.queue = list.slice(); P.shuffle = !!shuffle;
    const s = shuffle && start < 0 ? Math.floor(Math.random() * list.length) : Math.max(0, start);
    P.order = P.shuffle ? L.shuffleOrder(list.length, s, Date.now()) : [];
    load(s);
  }
  const playKeys = (keys, shuffle) => playList((keys || []).map(track).filter(Boolean), shuffle ? -1 : 0, shuffle);
  function enqueue(list) {
    if (!list.length) return;
    if (!P.queue.length) { playList(list, 0); return; }
    const n = P.queue.length; P.queue.push(...list);
    if (P.shuffle) P.order.push(...list.map((_, i) => n + i));
    sync(); report(true);
  }
  function toggle() { const a = audio(); if (!cur()) { const s = lib.songs || []; if (s.length) playList(s, 0); return; } a.paused ? (graph(), a.play().catch(() => {})) : a.pause(); }
  async function next(auto) {
    if (!P.queue.length) return;
    if (P.sleepAt === -1 && auto) { P.sleepAt = 0; audio().pause(); return; }
    let n = auto ? L.nextIndex(P.idx, P.queue.length, P.repeat, P.order) : L.nextIndex(P.idx, P.queue.length, P.repeat === "one" ? "all" : P.repeat, P.order);
    if (n < 0 && P.dj) { const more = (await API().MediaDJ(10) || []).filter(t => !P.queue.some(q => q.k === t.k)); if (more.length) { enqueue(more); n = L.nextIndex(P.idx, P.queue.length, P.repeat, P.order); } }
    if (n < 0) { if (!auto) return; sync(); report(true); return; }
    load(n);
  }
  function prev() {
    const a = audio();
    if (a.currentTime > 3) { a.currentTime = 0; return; }
    const p = L.prevIndex(P.idx, P.queue.length, P.repeat, P.order);
    if (p >= 0) load(p); else a.currentTime = 0;
  }
  function setShuffle(on) {
    P.shuffle = on;
    P.order = on && P.queue.length ? L.shuffleOrder(P.queue.length, Math.max(0, P.idx), Date.now()) : [];
    sync(); report(true);
  }
  const cycleRepeat = () => { P.repeat = L.nextRepeat(P.repeat); sync(); report(true); };
  function setVolume(v) { P.volume = Math.max(0, Math.min(1, v)); tick(); sync(); }
  function seekTo(ms) { const a = audio(); if (isFinite(a.duration)) a.currentTime = Math.max(0, Math.min(a.duration, ms / 1000)); }
  function setSleep(min) { P.sleepAt = min === -1 ? -1 : min > 0 ? Date.now() + min * 60000 : 0; sync(); }
  function removeAt(i) {
    if (i === P.idx || i < 0 || i >= P.queue.length) return;
    P.queue.splice(i, 1);
    if (i < P.idx) P.idx--;
    if (P.order.length) P.order = P.order.filter(x => x !== i).map(x => (x > i ? x - 1 : x));
    sync();
  }
  function tick() {
    const a = P.audio; if (!a) return;
    const pos = a.currentTime * 1000, dur = (isFinite(a.duration) ? a.duration : 0) * 1000;
    let v = P.volume * L.fadeVolume(pos, dur, P.fade * 1000);
    if (P.sleepAt > 0) {
      const left = P.sleepAt - Date.now();
      if (left <= 0) { P.sleepAt = 0; a.pause(); toast("Sleep timer: music stopped"); }
      else if (P.sleepFade) v *= L.sleepVolume(left);
    }
    if (P.gain) { P.gain.gain.value = v; a.volume = 1; } else a.volume = v;
    progress();
    if (Date.now() - P.lastReport > 3000) report();
  }
  function state() {
    const a = P.audio, t = cur();
    const up = L.upNext(P.idx, P.queue.length, P.repeat, P.order, 3).map(i => P.queue[i] && P.queue[i].t).filter(Boolean);
    return { kind: t ? "audio" : "", key: t ? t.k : "", title: t ? t.t : "", artist: t ? t.a || "" : "", playing: !!(a && !a.paused), pos_ms: a ? Math.round(a.currentTime * 1000) : 0,
      dur_ms: a && isFinite(a.duration) ? Math.round(a.duration * 1000) : (t ? t.d || 0 : 0), index: Math.max(0, P.idx), count: P.queue.length, shuffle: P.shuffle, repeat: P.repeat,
      volume: P.volume, sub_id: "", up_next: up };
  }
  function report(force) {
    if (window.BlazeVideo && BlazeVideo.active() && !(P.audio && !P.audio.paused)) return; // the video player reports while it's in charge
    if (!force && Date.now() - P.lastReport < 3000) return;
    P.lastReport = Date.now();
    try { API().MediaReport(state()); } catch (e) {}
  }
  // OS media keys / now-playing (where the webview supports Media Session).
  function session() {
    const ms = navigator.mediaSession; if (!ms) return;
    const t = cur(); if (!t) return;
    try {
      const art = artURL(t);
      ms.metadata = new MediaMetadata({ title: t.t, artist: t.a || "", album: t.al || "Blaze Music", artwork: art ? [{ src: art, sizes: "512x512" }] : [] });
      ms.setActionHandler("play", () => toggle()); ms.setActionHandler("pause", () => audio().pause());
      ms.setActionHandler("nexttrack", () => next(false)); ms.setActionHandler("previoustrack", () => prev());
      ms.setActionHandler("seekto", d => seekTo((d.seekTime || 0) * 1000));
    } catch (e) {}
  }

  // The agent (music_play / music_ops) drives the player.
  async function cmd(c) {
    if (!c || !c.op) return;
    await init();
    if (["play", "queue", "dj"].includes(c.op) && (c.keys || []).some(k => !track(k))) await reload();
    const list = (c.keys || []).map(track).filter(Boolean);
    switch (c.op) {
      case "play": if (list.length) playList(list, c.shuffle ? -1 : 0, c.shuffle); break;
      case "queue": enqueue(list); toast(`Added ${list.length} to the queue`); break;
      case "pause": audio().pause(); break;
      case "resume": if (cur()) { graph(); audio().play().catch(() => {}); } break;
      case "toggle": toggle(); break;
      case "next": next(false); break;
      case "prev": prev(); break;
      case "stop": audio().pause(); seekTo(0); break;
      case "shuffle": setShuffle(c.value === "on"); break;
      case "repeat": P.repeat = c.value || "all"; sync(); break;
      case "volume": setVolume(c.num); break;
      case "seek": seekTo((c.num || 0) * 1000); break;
      case "eq": applyEQ(c.value); sync(); break;
      case "crossfade": P.fade = c.num || 0; sync(); break;
      case "sleep": setSleep(c.num || 0); break;
      case "dj": P.dj = c.value === "on"; if (P.dj && list.length && !(P.audio && !P.audio.paused)) playList(list, 0); sync(); break;
      case "video": case "subs": case "subs_off": if (window.BlazeVideo) BlazeVideo.cmd(c); break;
    }
  }

  // ---------------------------------------------------------------- shared bits for both views
  const cover = (t, cls = "") => {
    const a = artURL(t);
    return a ? `<span class="mzart ${cls}"><img src="${esc(a)}" alt="" loading="lazy" onerror="this.parentNode.classList.add('none');this.remove()"></span>`
      : `<span class="mzart none ${cls}">${mi(t && t.kind === "video" ? "film" : "note")}</span>`;
  };
  const meta = t => [t.a, t.d ? L.clock(t.d) : ""].filter(Boolean).join(" \u00b7 ");

  function menu(anchor, items) {
    openPop(anchor, p => {
      p.classList.add("mzmenu");
      items.filter(Boolean).forEach(([label, fn, cls]) => {
        const b = h("button", "mi " + (cls || ""), esc(label));
        b.onclick = e => { e.stopPropagation(); closePop(); fn(); };
        p.append(b);
      });
    }, { width: 230 });
  }
  function dialog(title, body, actions) {
    document.querySelectorAll(".mzsheet-bg").forEach(x => x.remove());
    const bg = h("div", "mzsheet-bg"), s = h("div", "mzsheet glass-high");
    s.innerHTML = `<div class="mzsh-h"><span class="t">${esc(title)}</span><button class="mzround ghost" aria-label="Close">${mi("x")}</button></div><div class="mzsh-b"></div><div class="mzsh-a"></div>`;
    const close = () => { bg.classList.remove("in"); setTimeout(() => bg.remove(), 200); };
    $(".mzsh-h button", s).onclick = close;
    bg.onclick = e => { if (e.target === bg) close(); };
    if (typeof body === "string") $(".mzsh-b", s).innerHTML = body; else if (body) $(".mzsh-b", s).append(body);
    (actions || []).forEach(([label, fn, cls]) => { const b = h("button", "btn " + (cls || "line"), esc(label)); b.onclick = () => { if (fn() !== false) close(); }; $(".mzsh-a", s).append(b); });
    bg.append(s); document.body.append(bg);
    requestAnimationFrame(() => bg.classList.add("in"));
    return { el: s, close };
  }
  function askName(title, start, done) {
    const f = h("input", "field mzfield"); f.value = start || ""; f.placeholder = "Playlist name";
    const d = dialog(title, f, [["Cancel", () => {}, "ghost"], ["Save", () => { if (!f.value.trim()) return false; done(f.value.trim()); }, "accent"]]);
    setTimeout(() => f.focus(), 50);
    f.onkeydown = e => { if (e.key === "Enter") { done(f.value.trim()); d.close(); } };
  }
  function addToPlaylist(keys) {
    const box = h("div", "mzpick");
    (lib.playlists || []).forEach(p => {
      const b = h("button", "mzrow", `<span class="mzart none sm">${mi("queue")}</span><span class="m"><span class="t">${esc(p.name)}</span><span class="s">${p.keys.length} song${p.keys.length === 1 ? "" : "s"}</span></span>`);
      b.onclick = async () => { await API().MediaPlaylistAdd(p.id, keys); toast("Added to " + p.name); d.close(); };
      box.append(b);
    });
    if (!(lib.playlists || []).length) box.append(h("div", "mzempty sm", "No playlists yet."));
    const d = dialog("Add to playlist", box, [["Cancel", () => {}, "ghost"], ["New playlist", () => { askName("New playlist", "", async n => { await API().MediaPlaylistCreate(n, keys); toast("Added to " + n); }); }, "accent"]]);
  }
  function songMenu(anchor, t, pl, pi) {
    menu(anchor, [
      [liked(t.k) ? "Remove from Liked" : "Like", () => API().MediaLike(t.k)],
      ["Play next", () => { if (!P.queue.length) playList([t]); else { P.queue.splice(P.idx + 1, 0, t); if (P.order.length) { P.order = P.order.map(x => (x > P.idx ? x + 1 : x)); P.order.splice(P.order.indexOf(P.idx) + 1, 0, P.idx + 1); } sync(); toast("Plays next"); } }],
      ["Add to queue", () => { enqueue([t]); toast("Added to the queue"); }],
      ["Add to playlist\u2026", () => addToPlaylist([t.k])],
      pl && ["Remove from this playlist", () => API().MediaPlaylistRemoveAt(pl.id, pi)],
      pl && pi > 0 && ["Move up", () => API().MediaPlaylistMove(pl.id, pi, pi - 1)],
      pl && pi < pl.keys.length - 1 && ["Move down", () => API().MediaPlaylistMove(pl.id, pi, pi + 1)],
      ["Rename\u2026", () => renameTrack(t)],
      [t.src === "download" ? "Delete from this PC" : "Delete file\u2026", () => confirmDelete(t), "warn"],
    ]);
  }
  function renameTrack(t) {
    const box = h("div", "mzform");
    box.innerHTML = `<label><span class="label">Title</span><input class="field mzfield" value="${esc(t.t)}"></label><label><span class="label">Artist</span><input class="field mzfield" value="${esc(t.a || "")}"></label>`;
    const [ti, ar] = $$("input", box);
    dialog("Rename", box, [["Cancel", () => {}, "ghost"], ["Save", () => { API().MediaRename(t.k, ti.value, ar.value); }, "accent"]]);
  }
  function confirmDelete(t) {
    const own = t.src !== "download";
    dialog(`Delete \u201c${t.t.slice(0, 40)}\u201d?`, own ? `It's in your own folder (${esc((t.u || "").split(/[\\/]/).slice(-2, -1)[0] || "")}). Blaze removes the file from this PC and from every playlist.` : "It's removed from this PC and from every playlist.",
      [["Cancel", () => {}, "ghost"], ["Delete", () => { API().MediaDelete(t.k, own).then(err => err && toast(err)); }, "accent warn"]]);
  }

  // ---------------------------------------------------------------- the Music view
  let tab = "library", section = "foryou", q = "", openGroup = null, openList = null, panel = "art";
  let root = null;
  function musicView(el, arg) {
    if (arg) { const [a, b] = String(arg).split(":"); if (["library", "store", "downloads"].includes(a)) tab = a; if (b) section = b; }
    root = h("div", "mz mzmusic");
    el.append(root);
    init().then(paint);
    paint();
  }
  function paint() {
    if (!root || !root.isConnected) return;
    const songs = lib.songs || [];
    root.innerHTML = `
      <div class="mzhead">
        <div class="mzh-t"><span class="label acc">Blaze Music</span><h1>${tab === "store" ? "Find music" : tab === "downloads" ? "Downloads" : "Your music"}</h1></div>
        <div class="seg mzpill" role="tablist">${[["library", "Library"], ["store", "Store"], ["downloads", "Downloads"]].map(([id, n]) => `<button data-t="${id}" class="${tab === id ? "on" : ""}">${n}${id === "downloads" && dlActive ? ` <span class="mzbadge">${dlActive}</span>` : ""}</button>`).join("")}</div>
        <span class="grow"></span>
        <button class="btn ghost sm mzswitch" title="Blaze Video">${mi("film", "i sm")}Video</button>
        ${tab === "library" ? `<button class="btn line sm mzfolders" title="Music folders">${mi("folder", "i sm")}Folders</button>` : ""}
      </div>
      <div class="mzbody"><div class="mzmain"></div><aside class="mznp glass"></aside></div>`;
    $$(".mzpill button", root).forEach(b => b.onclick = () => { tab = b.dataset.t; paint(); });
    $(".mzswitch", root).onclick = () => show("video");
    const fb = $(".mzfolders", root); if (fb) fb.onclick = e => { e.stopPropagation(); folders(fb); };
    const main = $(".mzmain", root);
    if (tab === "store") BlazeMediaStore.view(main, "audio", { play: playStream });
    else if (tab === "downloads") BlazeMediaStore.downloads(main, "audio");
    else library(main, songs);
    nowPlaying($(".mznp", root));
  }

  function folders(anchor) {
    openPop(anchor, p => {
      p.classList.add("mzfolderpop");
      const draw = () => {
        const def = !(lib.prefs || {}).no_defaults;
        p.innerHTML = `<div class="phd"><span class="label">Scanned folders</span><span class="grow"></span>${lib.scanning ? '<span class="label weak">Scanning\u2026</span>' : ""}</div>
          <div class="mzflist">${(lib.folders || []).map(f => `<div class="mzf"><span class="p" title="${esc(f)}">${esc(f)}</span>${(lib.prefs.folders || []).includes(f) ? `<button class="mzround ghost sm" data-rm="${esc(f)}" aria-label="Remove">${mi("x", "i sm")}</button>` : ""}</div>`).join("")}</div>
          <label class="toggle mzdef"><input type="checkbox" ${def ? "checked" : ""}><span><span class="tt">Music and Videos folders</span><span class="td">Scan your home Music and Videos folders. Blaze's downloads are always included.</span></span></label>
          <div class="mzfacts"><button class="btn line sm add">${mi("plus", "i sm")}Add folder</button><button class="btn ghost sm scan">${mi("retry", "i sm")}Rescan</button></div>`;
        $$("[data-rm]", p).forEach(b => b.onclick = async () => { lib.folders = await API().MediaRemoveFolder(b.dataset.rm); lib.prefs.folders = (lib.prefs.folders || []).filter(x => x !== b.dataset.rm); draw(); });
        $(".add", p).onclick = async () => { lib.folders = await API().MediaAddFolder(); await reload(); draw(); };
        $(".scan", p).onclick = () => { API().MediaScan(); lib.scanning = true; draw(); };
        $(".mzdef input", p).onchange = async e => { lib.folders = await API().MediaDefaultFolders(e.target.checked); lib.prefs.no_defaults = !e.target.checked; draw(); };
      };
      draw();
    }, { width: 360 });
  }

  function library(main, songs) {
    const sections = [["foryou", "For you"], ["songs", "Songs"], ["liked", "Liked"], ["artists", "Artists"], ["albums", "Albums"], ["playlists", "Playlists"]];
    main.innerHTML = `<div class="mzbar"><label class="mzsearch">${mi("search", "i sm")}<input placeholder="Search your music" value="${esc(q)}"></label>
      <div class="mzchips">${sections.map(([id, n]) => `<button class="mzchip ${section === id && !q ? "on" : ""}" data-s="${id}">${n}</button>`).join("")}</div></div><div class="mzlist"></div>`;
    const inp = $(".mzsearch input", main);
    inp.oninput = () => { q = inp.value; draw(); $$(".mzchip", main).forEach(c => c.classList.toggle("on", !q && c.dataset.s === section)); };
    $$(".mzchip", main).forEach(c => c.onclick = () => { section = c.dataset.s; q = ""; inp.value = ""; openGroup = null; openList = null; library(main, songs); });
    const box = $(".mzlist", main);
    const draw = () => {
      box.innerHTML = "";
      if (!songs.length && !q) { emptyLibrary(box); return; }
      if (q) { const f = L.search(songs, q); rows(box, f, null); if (!f.length) box.append(h("div", "mzempty", `<b>No match</b>Nothing in your library matches \u201c${esc(q)}\u201d. Try the Store.`)); return; }
      if (section === "foryou") forYou(box, songs);
      else if (section === "songs") { box.append(listHead(`All songs \u00b7 ${songs.length}`, songs)); rows(box, songs); }
      else if (section === "liked") { const l = (lib.liked || []).map(track).filter(Boolean); box.append(listHead(`Liked \u00b7 ${l.length}`, l)); if (!l.length) box.append(h("div", "mzempty", "<b>No liked songs</b>Tap the heart on the player to keep a song here.")); rows(box, l); }
      else if (section === "artists" || section === "albums") groups(box, section === "artists" ? L.artists(songs) : L.albums(songs));
      else playlists(box);
    };
    draw();
  }
  function emptyLibrary(box) {
    box.append(h("div", "mzempty big", `<span class="mzemblem"><blaze-emblem size="44" ${lib.scanning ? "live" : ""}></blaze-emblem></span><b>${lib.scanning ? "Looking through your music folders\u2026" : "No songs yet"}</b>Songs in your Music folder (and folders you add) show up here and play offline. Find more in the Store, or ask Blaze to download some.`));
    const acts = h("div", "mzacts center");
    acts.innerHTML = `<button class="btn accent sm st">${mi("search", "i sm")}Open the Store</button><button class="btn line sm fo">${mi("folder", "i sm")}Add a folder</button>`;
    $(".st", acts).onclick = () => { tab = "store"; paint(); };
    $(".fo", acts).onclick = async () => { await API().MediaAddFolder(); reload(); };
    box.append(acts);
  }
  function listHead(title, list, extra) {
    const d = h("div", "mzlh", `<span class="t">${esc(title)}</span><span class="grow"></span>`);
    if (list.length) {
      const pl = h("button", "btn accent sm", `${mi("play", "i sm")}Play`); pl.onclick = () => playList(list, 0);
      const sh = h("button", "btn line sm", `${mi("shuffle", "i sm")}Shuffle`); sh.onclick = () => playList(list, -1, true);
      d.append(pl, sh);
    }
    if (extra) extra.forEach(x => d.append(x));
    return d;
  }
  function rows(box, list, pl) {
    const t0 = cur();
    list.slice(0, 600).forEach((t, i) => {
      const r = h("div", "mzrow" + (t0 && t0.k === t.k ? " on" : ""));
      r.innerHTML = `${cover(t, "sm")}<span class="m"><span class="t">${esc(t.t)}</span><span class="s">${esc(meta(t) || (t.src === "download" ? "Downloaded" : "Your files"))}</span></span>${liked(t.k) ? `<span class="mzlike">${mi("heartf", "i sm")}</span>` : ""}<button class="mzround ghost sm" aria-label="More">${mi("more")}</button>`;
      r.onclick = e => { if (e.target.closest("button")) return; playList(list, i); };
      $("button", r).onclick = e => { e.stopPropagation(); songMenu(e.currentTarget, t, pl, i); };
      box.append(r);
    });
  }
  function groups(box, gs) {
    if (openGroup) {
      const g = gs.find(x => x[0] === openGroup);
      if (g) {
        const back = h("button", "btn ghost sm", `${mi("prev", "i sm")}All`); back.onclick = () => { openGroup = null; box.innerHTML = ""; groups(box, gs); };
        box.append(listHead(`${g[0]} \u00b7 ${g[1].length}`, g[1], [back])); rows(box, g[1]); return;
      }
    }
    if (!gs.length) box.append(h("div", "mzempty", "<b>Nothing here yet</b>Add music to your folders or download some in the Store."));
    const grid = h("div", "mzgrid");
    gs.forEach(([name, list]) => {
      const c = h("button", "mztile", `${cover(list.find(t => t.art) || list[0], "lg")}<span class="t">${esc(name)}</span><span class="s">${list.length} song${list.length === 1 ? "" : "s"}</span>`);
      c.onclick = () => { openGroup = name; box.innerHTML = ""; groups(box, gs); };
      grid.append(c);
    });
    box.append(grid);
  }
  function playlists(box) {
    const pls = lib.playlists || [];
    if (openList) {
      const p = pls.find(x => x.id === openList);
      if (p) {
        const list = p.keys.map(track).filter(Boolean);
        const back = h("button", "btn ghost sm", `${mi("prev", "i sm")}All`); back.onclick = () => { openList = null; box.innerHTML = ""; playlists(box); };
        const ren = h("button", "mzround ghost sm", mi("more")); ren.setAttribute("aria-label", "Playlist options");
        ren.onclick = e => { e.stopPropagation(); menu(ren, [["Rename\u2026", () => askName("Rename playlist", p.name, n => API().MediaPlaylistRename(p.id, n))], ["Delete playlist", () => { API().MediaPlaylistDelete(p.id); openList = null; }, "warn"]]); };
        box.append(listHead(`${p.name} \u00b7 ${list.length}`, list, [back, ren]));
        if (!list.length) box.append(h("div", "mzempty", "<b>Empty playlist</b>Use \u22ef \u2192 Add to playlist on any song."));
        rows(box, list, p); return;
      }
    }
    const nb = h("button", "mzrow", `<span class="mzart none sm">${mi("plus")}</span><span class="m"><span class="t">New playlist</span></span>`);
    nb.onclick = () => askName("New playlist", "", n => API().MediaPlaylistCreate(n, []));
    box.append(nb);
    pls.forEach(p => {
      const first = p.keys.map(track).find(t => t && t.art);
      const r = h("button", "mzrow", `${first ? cover(first, "sm") : `<span class="mzart none sm">${mi("queue")}</span>`}<span class="m"><span class="t">${esc(p.name)}</span><span class="s">${p.keys.length} song${p.keys.length === 1 ? "" : "s"}</span></span>`);
      r.onclick = () => { openList = p.id; box.innerHTML = ""; playlists(box); };
      box.append(r);
    });
  }
  async function forYou(box, songs) {
    const recent = (lib.recent || []).map(track).filter(Boolean).slice(0, 12);
    // Blaze DJ + Wrapped + tools
    const hero = h("div", "mzhero glass");
    hero.innerHTML = `<div class="mzhero-l"><span class="mzemblem"><blaze-emblem size="34" ${P.dj ? "live" : ""}></blaze-emblem></span><div><div class="t">Blaze DJ</div><div class="s">${P.dj ? "On: keeps the music going with songs you love." : "Plays the songs you love and keeps adding more when the queue runs out."}</div></div></div>
      <div class="mzhero-a"><button class="btn ${P.dj ? "line" : "accent"} sm dj">${mi("dj", "i sm")}${P.dj ? "Stop DJ" : "Start DJ"}</button><button class="btn line sm wr">${mi("spark", "i sm")}Wrapped</button><button class="mzround ghost sm more" aria-label="More">${mi("more")}</button></div>`;
    $(".dj", hero).onclick = async () => { P.dj = !P.dj; API().MediaSetPref("dj", String(P.dj)); if (P.dj) { const t = await API().MediaDJ(10) || []; if (t.length && !(P.audio && !P.audio.paused)) playList(t, 0); } paint(); };
    $(".wr", hero).onclick = () => wrapped("year");
    $(".more", hero).onclick = e => { e.stopPropagation(); menu(e.currentTarget, [["Import a playlist (YouTube or Spotify)\u2026", importDialog], ["Fix song titles and covers", async () => toast(await API().MediaFixTags(), 4200)]]); };
    box.append(hero);
    if (recent.length) {
      box.append(h("div", "mzlh", '<span class="t">Recently played</span>'));
      const strip = h("div", "mzstrip");
      recent.forEach((t, i) => { const c = h("button", "mztile sm", `${cover(t, "lg")}<span class="t">${esc(t.t)}</span><span class="s">${esc(t.a || "")}</span>`); c.onclick = () => playList(recent, i); strip.append(c); });
      box.append(strip);
    }
    let mixes = [];
    try { mixes = await API().MediaMixes() || []; } catch (e) {}
    if (!box.isConnected) return;
    if (mixes.length) {
      box.append(h("div", "mzlh", '<span class="t">Mixes and smart playlists</span>'));
      const grid = h("div", "mzgrid");
      mixes.forEach(m => {
        const list = m.keys.map(track).filter(Boolean); if (!list.length) return;
        const arts = list.filter(t => t.art).slice(0, 4);
        const c = h("button", "mztile mix", `<span class="mzart lg mosaic">${arts.length >= 4 ? arts.map(t => `<img src="${esc(artURL(t))}" alt="">`).join("") : arts.length ? `<img class="one" src="${esc(artURL(arts[0]))}" alt="">` : mi(m.id.startsWith("mix") ? "dj" : "spark")}</span><span class="t">${esc(m.name)}</span><span class="s">${esc(m.about)} \u00b7 ${list.length}</span>`);
        c.onclick = () => playList(list, m.id.startsWith("mix") ? -1 : 0, m.id.startsWith("mix"));
        grid.append(c);
      });
      box.append(grid);
    }
    box.append(listHead(`All songs \u00b7 ${songs.length}`, songs));
    rows(box, songs.slice(0, 40));
  }
  async function wrapped(period) {
    const w = await API().MediaWrapped(period);
    const body = h("div", "mzwrap");
    const seg = h("div", "seg", ["month", "year", "all"].map(p => `<button data-p="${p}" class="${p === period ? "on" : ""}">${p === "month" ? "This month" : p === "year" ? "This year" : "All time"}</button>`).join(""));
    body.append(seg);
    if (!w) body.append(h("div", "mzempty", "<b>Nothing played yet</b>Wrapped counts songs you play in Blaze Music."));
    else {
      body.append(h("div", "mzstats", [["Minutes", w.minutes], ["Plays", w.plays], ["Songs", w.songs], ["Artists", w.artists]].map(([k, v]) => `<div class="st"><span class="v">${v}</span><span class="label">${k}</span></div>`).join("")));
      if ((w.top_songs || []).length) body.append(h("div", "mzwl", `<span class="label">Top songs</span>${w.top_songs.map((s, i) => `<div class="r"><span class="n">${i + 1}</span><span class="t">${esc(s.track.t)}</span><span class="s">${s.plays} plays</span></div>`).join("")}`));
      if ((w.top_artists || []).length) body.append(h("div", "mzwl", `<span class="label">Top artists</span>${w.top_artists.map((a, i) => `<div class="r"><span class="n">${i + 1}</span><span class="t">${esc(a.name)}</span><span class="s">${a.minutes} min</span></div>`).join("")}`));
      body.append(h("p", "mzsmall", `You listen most around ${String(w.busiest_hour).padStart(2, "0")}:00.`));
    }
    const d = dialog("Wrapped \u00b7 " + (w ? w.label : ""), body, [["Close", () => {}, "line"]]);
    $$("button", seg).forEach(b => b.onclick = () => { d.close(); wrapped(b.dataset.p); });
  }
  function importDialog() {
    const f = h("input", "field mzfield"); f.placeholder = "https://open.spotify.com/playlist/\u2026 or a YouTube playlist link";
    const box = h("div", "mzform"); box.append(h("p", "mzsmall", "Every song is found on YouTube and downloaded into Blaze Music; a playlist with the same name fills as they finish."), f);
    dialog("Import a playlist", box, [["Cancel", () => {}, "ghost"], ["Import", () => { const v = f.value.trim(); if (!v) return false; toast("Importing\u2026"); API().MediaImport(v).then(r => toast(r, 5000)); }, "accent"]]);
    setTimeout(() => f.focus(), 50);
  }

  // ---------------------------------------------------------------- now playing (right column)
  let npEl = null, lyr = { key: "", data: null, err: "" };
  function nowPlaying(el) {
    npEl = el;
    const t = cur();
    if (!t) {
      el.innerHTML = `<div class="mznp-empty"><span class="mzemblem"><blaze-emblem size="40"></blaze-emblem></span><b>Nothing playing</b><span>Pick a song, or ask Blaze: \u201cplay something chill\u201d.</span></div>`;
      return;
    }
    const a = audio(), playing = !a.paused;
    el.innerHTML = `
      <div class="mznp-top"><span class="label">${playing ? "Now playing" : "Paused"}</span><span class="grow"></span><blaze-emblem size="18" ${playing ? "live" : "dim"}></blaze-emblem></div>
      <div class="mznp-stage">${panel === "art" ? cover(t, "xl") : ""}<div class="mznp-panel"></div></div>
      <div class="mznp-meta"><div class="m"><div class="t">${esc(t.t)}</div><div class="s">${esc(t.a || (t.stream ? "Streaming" : "Unknown artist"))}</div></div>
        <button class="mzround ghost like ${liked(t.k) ? "on" : ""}" aria-label="${liked(t.k) ? "Unlike" : "Like"}">${mi(liked(t.k) ? "heartf" : "heart")}</button></div>
      ${P.error ? `<div class="mzerr">${esc(P.error)} <button class="btn ghost xs skip">Skip</button></div>` : ""}
      <div class="mzprog"><input type="range" min="0" max="1000" value="0" aria-label="Seek"><div class="tm"><span class="a">0:00</span><span class="b">${L.clock(t.d)}</span></div></div>
      <div class="mzctl">
        <button class="mzround ghost sh ${P.shuffle ? "on" : ""}" aria-label="Shuffle ${P.shuffle ? "on" : "off"}">${mi("shuffle")}</button>
        <button class="mzround lg pv" aria-label="Previous">${mi("prev")}</button>
        <button class="mzround xl pp" aria-label="${playing ? "Pause" : "Play"}">${mi(playing ? "pause" : "play")}</button>
        <button class="mzround lg nx" aria-label="Next">${mi("next")}</button>
        <button class="mzround ghost rp ${P.repeat !== "off" ? "on" : ""}" aria-label="Repeat ${P.repeat}">${mi("repeat")}${P.repeat === "one" ? '<span class="one">1</span>' : ""}</button>
      </div>
      <div class="mzchips np">
        <button class="mzchip ${panel === "lyrics" ? "on" : ""}" data-p="lyrics">${mi("lyrics", "i sm")}Lyrics</button>
        <button class="mzchip ${panel === "karaoke" ? "on" : ""}" data-p="karaoke">${mi("mic", "i sm")}Karaoke</button>
        <button class="mzchip ${panel === "queue" ? "on" : ""}" data-p="queue">${mi("queue", "i sm")}Queue</button>
        <button class="mzchip ${P.sleepAt ? "on" : ""}" data-x="sleep">${mi("sleep", "i sm")}<span class="sl">${sleepLabel()}</span></button>
        <button class="mzchip ${P.eq !== "Flat" ? "on" : ""}" data-x="eq">${mi("eq", "i sm")}${P.eq === "Flat" ? "EQ" : esc(P.eq)}</button>
        <button class="mzchip ${P.fade ? "on" : ""}" data-x="fade">${mi("fade", "i sm")}${P.fade ? "Fade " + P.fade + " s" : "Fade"}</button>
        <label class="mzvol" title="Volume">${mi("vol", "i sm")}<input type="range" min="0" max="100" value="${Math.round(P.volume * 100)}" aria-label="Volume"></label>
      </div>`;
    $(".pp", el).onclick = toggle; $(".nx", el).onclick = () => next(false); $(".pv", el).onclick = prev;
    $(".sh", el).onclick = () => setShuffle(!P.shuffle); $(".rp", el).onclick = cycleRepeat;
    $(".like", el).onclick = () => { if (t.stream) toast("Download it first to keep it in Liked."); else API().MediaLike(t.k); };
    const sk = $(".skip", el); if (sk) sk.onclick = () => next(false);
    const r = $(".mzprog input", el);
    r.oninput = () => { r.dataset.drag = "1"; const d = audio().duration; if (isFinite(d)) $(".mzprog .a", el).textContent = L.clock(r.value / 1000 * d * 1000); };
    r.onchange = () => { delete r.dataset.drag; const d = audio().duration; if (isFinite(d)) audio().currentTime = r.value / 1000 * d; };
    $(".mzvol input", el).oninput = e => { setVolume(e.target.value / 100); API().MediaSetPref("volume", e.target.value); };
    $$("[data-p]", el).forEach(b => b.onclick = () => { panel = panel === b.dataset.p ? "art" : b.dataset.p; nowPlaying(el); });
    $("[data-x=sleep]", el).onclick = e => { e.stopPropagation(); menu(e.currentTarget, [P.sleepAt && ["Turn off", () => setSleep(0)], ...info.sleep.map(m => [`${m} minutes`, () => setSleep(m)]), ["End of this song", () => setSleep(-1)],
      [`Fade out at the end: ${P.sleepFade ? "on" : "off"}`, () => { P.sleepFade = !P.sleepFade; API().MediaSetPref("sleep_fade", String(P.sleepFade)); }]]); };
    $("[data-x=eq]", el).onclick = e => { e.stopPropagation(); menu(e.currentTarget, info.eq.map(n => [n + (n === P.eq ? "  \u2713" : ""), () => { applyEQ(n); API().MediaSetPref("eq", n); nowPlaying(el); }])); };
    $("[data-x=fade]", el).onclick = e => { e.stopPropagation(); menu(e.currentTarget, [0, 3, 6, 10].map(s => [(s ? `Fade ${s} s between songs` : "No fade between songs") + (s === P.fade ? "  \u2713" : ""), () => { P.fade = s; API().MediaSetPref("crossfade", String(s)); nowPlaying(el); }])); };
    const pn = $(".mznp-panel", el);
    if (panel === "queue") queueView(pn);
    else if (panel === "lyrics" || panel === "karaoke") lyricsView(pn, t, panel === "karaoke");
    progress();
  }
  const sleepLabel = () => (P.sleepAt === -1 ? "End of song" : P.sleepAt > 0 ? L.clock(P.sleepAt - Date.now()) : "Sleep");
  function queueView(pn) {
    pn.classList.add("q");
    const t = cur();
    pn.innerHTML = `<div class="label">Now playing</div>`;
    const now = h("div", "mzrow on", `${cover(t, "sm")}<span class="m"><span class="t">${esc(t.t)}</span><span class="s">${esc(t.a || "")}</span></span>`); pn.append(now);
    const up = L.upNext(P.idx, P.queue.length, P.repeat, P.order, 60);
    pn.append(h("div", "label", up.length ? "Up next" + (P.shuffle ? " \u00b7 shuffled" : "") : "Nothing up next"));
    up.forEach(i => {
      const x = P.queue[i]; if (!x) return;
      const r = h("div", "mzrow", `${cover(x, "sm")}<span class="m"><span class="t">${esc(x.t)}</span><span class="s">${esc(x.a || "")}</span></span><button class="mzround ghost sm" aria-label="Remove from queue">${mi("x", "i sm")}</button>`);
      r.onclick = e => { if (e.target.closest("button")) { removeAt(i); return; } load(i); };
      pn.append(r);
    });
  }
  async function lyricsView(pn, t, karaoke) {
    pn.classList.add("ly");
    if (lyr.key !== t.k) { lyr = { key: t.k, data: null, err: "" }; pn.innerHTML = '<div class="mzsmall">Looking for lyrics\u2026</div>'; try { const r = await API().MediaLyrics(t.k); if (r && r.error) lyr.err = r.error; else lyr.data = r; } catch (e) { lyr.err = "Couldn't load lyrics."; } if (cur() !== t || !pn.isConnected) return; }
    const r = lyr.data;
    if (lyr.err) { pn.innerHTML = `<div class="mzsmall">${esc(lyr.err)}</div>`; const b = h("button", "btn line xs", "Try again"); b.onclick = () => { lyr.key = ""; lyricsView(pn, t, karaoke); }; pn.append(b); return; }
    if (!r || (!(r.synced || []).length && !r.plain && !r.instrumental)) { pn.innerHTML = '<div class="mzsmall">No lyrics found for this song.</div>'; return; }
    if (r.instrumental) { pn.innerHTML = '<div class="mzsmall big">Instrumental</div>'; return; }
    const lines = r.synced || [];
    if (karaoke) {
      if (!lines.length) { pn.innerHTML = '<div class="mzsmall">Karaoke needs synced lyrics, and this song only has plain ones.</div>'; return; }
      pn.innerHTML = `<div class="mzkara"><div class="now"><span class="fill"></span><span class="txt"></span></div><div class="nx"></div></div><div class="label weak src">Karaoke \u00b7 lyrics: LRCLIB</div>`;
    } else if (lines.length) {
      pn.innerHTML = `<div class="mzlyr">${lines.map((l, i) => `<p data-i="${i}">${esc(l.text) || "\u266a"}</p>`).join("")}</div><div class="label weak src">Lyrics: LRCLIB</div>`;
      $$("p", pn).forEach(p => p.onclick = () => seekTo(lines[+p.dataset.i].ms));
    } else pn.innerHTML = `<div class="mzlyr plain">${esc(r.plain).replace(/\n/g, "<br>")}</div><div class="label weak src">Lyrics: LRCLIB</div>`;
    pn._lines = lines; pn._kara = karaoke;
    progress();
  }
  // 4× a second: the progress bar, the lyric line, the sleep chip, the mini player.
  function progress() {
    const a = P.audio, t = cur(); if (!a || !t) return;
    const pos = a.currentTime * 1000, dur = isFinite(a.duration) ? a.duration * 1000 : t.d || 0;
    if (npEl && npEl.isConnected) {
      const r = $(".mzprog input", npEl);
      if (r && !r.dataset.drag) { r.value = dur ? Math.round(pos / dur * 1000) : 0; r.style.setProperty("--p", (dur ? pos / dur * 100 : 0) + "%"); $(".mzprog .a", npEl).textContent = L.clock(pos); $(".mzprog .b", npEl).textContent = L.clock(dur); }
      const sl = $(".sl", npEl); if (sl) sl.textContent = sleepLabel();
      const pn = $(".mznp-panel", npEl);
      if (pn && pn._lines && pn._lines.length) {
        const i = L.lrcCurrent(pn._lines, pos + 150);
        if (pn._kara) {
          const l = pn._lines[i], n = pn._lines[i + 1];
          const txt = $(".txt", pn); if (txt && txt.textContent !== ((l && l.text) || "\u266a")) { txt.textContent = (l && l.text) || "\u266a"; $(".fill", pn).dataset.t = txt.textContent; $(".nx", pn).textContent = n ? n.text : ""; }
          const f = $(".fill", pn); if (f && l) { const end = n ? n.ms : l.ms + 4000; f.style.setProperty("--k", Math.max(0, Math.min(100, (pos - l.ms) / Math.max(1, end - l.ms) * 100)) + "%"); }
        } else if (pn._cur !== i) {
          pn._cur = i;
          $$(".mzlyr p", pn).forEach((p, j) => p.classList.toggle("on", j === i));
          const on = $(".mzlyr p.on", pn); if (on) on.scrollIntoView({ block: "center", behavior: BlazeMotionOK() ? "smooth" : "auto" });
        }
      }
    }
    if (mini) { const b = $(".bar i", mini); if (b) b.style.width = (dur ? pos / dur * 100 : 0) + "%"; }
  }
  const BlazeMotionOK = () => !window.BlazeMotion || BlazeMotion.enabled !== false;

  // ---------------------------------------------------------------- mini player (stays on every tab)
  let mini = null;
  function sync() {
    if (npEl && npEl.isConnected) nowPlaying(npEl);
    else if (root && root.isConnected) { const n = $(".mznp", root); if (n) nowPlaying(n); }
    if (root && root.isConnected) $$(".mzrow", root).forEach(r => r.classList.remove("on"));
    miniSync();
    window.dispatchEvent(new CustomEvent("blazemedia:player", { detail: state() }));
  }
  function miniSync() {
    const t = cur(), hide = !t || view === "music";
    if (!mini) {
      mini = h("div", "mzmini glass-high"); mini.id = "mzMini";
      document.body.append(mini);
    }
    mini.classList.toggle("in", !hide);
    if (!t) return;
    const playing = P.audio && !P.audio.paused;
    mini.innerHTML = `<button class="open" title="Open Blaze Music">${cover(t, "sm")}<span class="m"><span class="t">${esc(t.t)}</span><span class="s">${esc(t.a || "Blaze Music")}</span></span></button>
      <button class="mzround sm pp" aria-label="${playing ? "Pause" : "Play"}">${mi(playing ? "pause" : "play", "i sm")}</button><button class="mzround ghost sm nx" aria-label="Next">${mi("next", "i sm")}</button><span class="bar"><i></i></span>`;
    $(".open", mini).onclick = () => show("music");
    $(".pp", mini).onclick = toggle; $(".nx", mini).onclick = () => next(false);
    progress();
  }
  // Show / hide the mini player when the view changes (no hook into show(): watch #view).
  const vw = document.getElementById("view");
  if (vw) new MutationObserver(() => miniSync()).observe(vw, { childList: true });

  // Store "Play": a stream through the proxy (not kept in the library).
  function playStream(item, v) {
    if (!v.stream) { toast("This one can't play right away. Download it instead."); return; }
    const t = { k: "stream:" + item.source + ":" + item.id, t: v.detail.title || item.title, a: v.detail.author || item.author, art: item.thumb, d: v.stream_ms, stream: v.stream, kind: "audio" };
    playList([t], 0);
  }

  // ---------------------------------------------------------------- downloads badge + events
  let dlActive = 0;
  async function dlCount() { try { const d = await API().MediaDownloads() || []; const n = d.filter(j => j.st === "QUEUED" || j.st === "RUNNING").length; if (n !== dlActive) { dlActive = n; if (root && root.isConnected && tab !== "downloads") { const b = $(".mzpill [data-t=downloads]", root); if (b) b.innerHTML = "Downloads" + (n ? ` <span class="mzbadge">${n}</span>` : ""); } } } catch (e) {} }
  if (window.runtime) {
    on("media:library", soon);
    on("media:scan", s => { lib.scanning = !!s; if (!s) soon(); });
    on("media:downloads", () => { dlCount(); window.dispatchEvent(new CustomEvent("blazemedia:downloads")); });
    on("media:cmd", c => (c && ["video", "subs", "subs_off"].includes(c.op) && window.BlazeVideo ? BlazeVideo.cmd(c) : cmd(c)));
    on("media:prefs", p => { if (p) lib.prefs = p; });
    on("media:work", w => { if (w) toast(w, 1800); });
  }
  setInterval(() => { if (P.audio && !P.audio.paused) tick(); else if (P.sleepAt > 0) tick(); }, 1000);

  // Prefs that live in Go: EQ, fade, sleep fade, DJ, volume.
  init().then(() => {
    const p = lib.prefs || {};
    P.eq = p.eq || "Flat"; P.fade = p.crossfade_s || 0; P.sleepFade = p.sleep_fade !== false; P.dj = !!p.dj; if (p.volume) P.volume = p.volume;
    dlCount();
  });

  // ---------------------------------------------------------------- entry points
  window.VIEWS = window.VIEWS || {};
  window.VIEWS.music = (el, arg) => musicView(el, arg);
  if (window.BlazeViews) {
    BlazeViews.LIST.push({ id: "music", name: "Music", icon: "music", desc: "Blaze Music: your songs, playlists, mixes, the Store", open: () => show("music") });
    BlazeViews.LIST.push({ id: "video", name: "Video", icon: "film", desc: "Blaze Video: your videos, series, subtitles, the Store", open: () => show("video") });
  }
  Apps.register({ id: "music", name: "Music", icon: MI.note, desc: "Your songs, playlists and mixes", open: () => show("music") });
  if (typeof COMMANDS !== "undefined") {
    COMMANDS.push({ name: "music", desc: "Blaze Music: your songs, playlists, the Store", run: () => show("music") });
    COMMANDS.push({ name: "video", desc: "Blaze Video: your videos, series, subtitles", run: () => show("video") });
  }
  // v0.19.14: the round Music | Video pair left the top bar (less chrome on the chat surface).
  // Both stay one click away in the account menu (shell.js), in Search and as /music and /video.

  return { init, reload, cmd, playList, playKeys, toggle, next, prev, state, cur, track, lib: () => lib, info: () => info, url, fileURL, artURL, cover, mi, menu, dialog, toast, meta, paint,
    pause: () => P.audio && P.audio.pause(), playing: () => !!(P.audio && !P.audio.paused) };
})();
window.BlazeMusic = BlazeMusic;
