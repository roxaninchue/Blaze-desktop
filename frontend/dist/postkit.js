// Post kit (v0.19.11, postkit_app.go + internal/postkit): give a photo or an idea, choose the
// platforms, and get a full blaze.postkit/1 kit: one hook, 3 hashtags, alt text, a caption per
// platform within its limit, and crops for 1:1, 4:5, 9:16 and 16:9. Copy any part, copy all,
// copy the JSON, or Export (kit JSON, captions and the cropped photos). Open checks a kit made by
// another Blaze app. Copy-out only: Blaze never posts for you.
(() => {
  let OPT = null;
  const st = { photo: null, idea: "", language: "en", platforms: null, kit: null, busy: false, req: 0, note: "", error: "", problems: [] };
  const cps = s => [...String(s || "")].length;
  const xlen = s => { let n = 0, last = 0; const re = /https?:\/\/[^\s<>"'\])]+/g; let m; s = String(s || ""); while ((m = re.exec(s))) { n += cps(s.slice(last, m.index)) + 23; last = m.index + m[0].length; } return n + cps(s.slice(last)); };

  async function opts() {
    if (OPT) return OPT;
    try { OPT = await API().PostKitOptions(); } catch (e) {}
    OPT = OPT || { platforms: [{ id: "x", name: "X", limit: 280 }, { id: "instagram", name: "Instagram", limit: 2200 }, { id: "tiktok", name: "TikTok", limit: 4000 }], languages: [{ tag: "en", name: "English" }], default_platforms: ["instagram", "tiktok", "x"] };
    return OPT;
  }

  async function copy(text, btn) {
    let ok = false;
    try { if (window.runtime && window.runtime.ClipboardSetText) ok = await window.runtime.ClipboardSetText(text); } catch (e) {}
    if (!ok) try { await navigator.clipboard.writeText(text); ok = true; } catch (e) {}
    if (btn) { const t = btn.textContent; btn.textContent = ok ? "Copied" : "Couldn't copy"; setTimeout(() => { btn.textContent = t; }, 1400); }
    return ok;
  }
  const copyBtn = (text, label = "Copy") => { const b = h("button", "btn ghost xs pk-copy", label); b.onclick = () => copy(text, b); return b; };

  async function postkitView(el) {
    const o = await opts();
    if (!st.platforms) st.platforms = [...(o.default_platforms || ["instagram", "tiktok", "x"])];
    const { col, acts } = page(el, "Creators", "Post kit", "Add a photo or type an idea. Blaze writes the hook, 3 hashtags, alt text and a caption for each platform you choose, and frames the photo for every format. You copy it out; Blaze never posts for you.");
    const openB = h("button", "btn line", "Open a kit");
    openB.onclick = async () => { const r = await API().PostKitOpen(); if (r && (r.kit || r.error)) { take(r); draw(); } };
    acts.append(openB);
    const form = h("div", "group pk-form");
    const result = h("div", "pk-result");
    col.append(form, result);
    const draw = () => { drawForm(form, o, draw); drawResult(result, draw); };
    draw();
  }

  function take(r) {
    st.error = r.error || ""; st.note = r.note || ""; st.problems = r.problems || [];
    if (r.kit) { st.kit = { kit: r.kit, json: r.json, text: r.text, photo: r.photo || "", thumb: r.photo && st.photo && st.photo.path === r.photo ? st.photo.thumb : "" }; }
    else if (r.error) st.kit = null;
  }

  async function setPhoto(p, redraw) {
    if (!p) return;
    if (p.error) { st.error = p.error; redraw(); return; }
    if (p.path) { st.photo = p; st.error = ""; redraw(); }
  }

  function readFile(file, redraw) {
    if (!file || !/^image\//.test(file.type || "")) return;
    const fr = new FileReader();
    fr.onload = async () => setPhoto(await API().PostKitPhotoPasted(file.name || "photo.png", String(fr.result)), redraw);
    fr.readAsDataURL(file);
  }

  function drawForm(g, o, redraw) {
    g.innerHTML = "";
    g.append(h("div", "gh", `<span class="label">Start</span>`));
    const box = h("div", "box pad pk-start");
    // Photo
    const ph = h("div", "pk-photo" + (st.photo ? " has" : ""));
    if (st.photo) {
      ph.innerHTML = `${st.photo.thumb ? `<img alt="" src="${esc(st.photo.thumb)}">` : ""}<div class="pk-pmeta"><b>${esc(st.photo.name)}</b><span>${st.photo.width} × ${st.photo.height}</span></div>`;
      const rm = h("button", "btn ghost xs", "Remove"); rm.onclick = () => { st.photo = null; redraw(); }; ph.append(rm);
    } else {
      ph.innerHTML = `<div class="pk-drop">Drop or paste a photo here, or</div>`;
      const add = h("button", "btn line xs pk-add", "Add photo");
      add.onclick = async () => setPhoto(await API().PostKitPickPhoto(), redraw);
      ph.append(add);
    }
    ph.addEventListener("dragover", e => { e.preventDefault(); ph.classList.add("over"); });
    ph.addEventListener("dragleave", () => ph.classList.remove("over"));
    ph.addEventListener("drop", e => { e.preventDefault(); e.stopPropagation(); ph.classList.remove("over"); readFile(e.dataTransfer && e.dataTransfer.files && e.dataTransfer.files[0], redraw); });
    box.append(ph);
    // Idea
    const idea = h("textarea", "field pk-idea");
    idea.placeholder = st.photo ? "Anything to say about it? (optional) Links and @handles are kept exactly." : "Your idea: what's the post about? Links and @handles are kept exactly.";
    idea.maxLength = 2000; idea.value = st.idea;
    idea.oninput = () => { st.idea = idea.value; };
    idea.addEventListener("paste", e => { const f = [...((e.clipboardData && e.clipboardData.files) || [])].find(x => /^image\//.test(x.type)); if (f) { e.preventDefault(); readFile(f, redraw); } });
    box.append(idea);
    // Platforms
    const pr = h("div", "pk-plats");
    (o.platforms || []).forEach(p => {
      const on = st.platforms.includes(p.id);
      const b = h("button", "pk-plat" + (on ? " on" : ""), esc(p.name));
      b.dataset.id = p.id; b.setAttribute("aria-pressed", on ? "true" : "false");
      b.onclick = () => { st.platforms = on ? st.platforms.filter(x => x !== p.id) : [...st.platforms, p.id]; redraw(); };
      pr.append(b);
    });
    box.append(pr);
    // Language + go
    const row = h("div", "pk-go");
    const lang = h("select", "field pk-lang");
    (o.languages || []).forEach(l => { const op = h("option", "", esc(l.name)); op.value = l.tag; if (l.tag === st.language) op.selected = true; lang.append(op); });
    lang.onchange = () => { st.language = lang.value; };
    const go = h("button", "btn accent pk-make", st.busy ? "Making your kit…" : (st.kit ? "Make it again" : "Make kit"));
    go.disabled = st.busy || (!st.photo && !st.idea.trim()) || !st.platforms.length;
    idea.addEventListener("input", () => { go.disabled = st.busy || (!st.photo && !idea.value.trim()) || !st.platforms.length; });
    go.onclick = () => make(redraw);
    row.append(lang, go);
    box.append(row);
    if (st.error) box.append(h("div", "pk-err", esc(st.error)));
    g.append(box);
  }

  async function make(redraw) {
    const id = ++st.req;
    st.busy = true; st.error = ""; st.note = ""; redraw();
    let r;
    try { r = await API().PostKitMake({ idea: st.idea, photo: st.photo ? st.photo.path : "", language: st.language, platforms: st.platforms }); }
    catch (e) { r = { error: "Blaze couldn't make the kit right now. Try again in a moment." }; }
    if (id !== st.req) return; // a newer request replaced this one
    st.busy = false;
    take(r || {});
    redraw();
  }

  function field(label, text, extra) {
    const f = h("div", "pk-field");
    const head = h("div", "pk-fh", `<span class="label">${esc(label)}</span>${extra ? `<span class="pk-count">${extra}</span>` : ""}`);
    head.append(copyBtn(text));
    f.append(head, h("div", "pk-val", esc(text)));
    return f;
  }

  function drawResult(el, redraw) {
    el.innerHTML = "";
    const K = st.kit;
    if (!K) return;
    const k = K.kit;
    const g = h("div", "group pk-kit");
    const right = h("span", "pk-acts");
    const exp = h("button", "btn accent xs pk-export", "Export…");
    exp.onclick = async () => {
      const r = await API().PostKitExport(K.json, K.photo || "");
      if (!r || (!r.dir && !r.error)) return;
      const msg = $(".pk-saved", g) || h("div", "pk-saved");
      msg.textContent = r.error ? r.error : `Saved ${r.files.length} files in ${r.dir}`;
      msg.classList.toggle("bad", !!r.error);
      g.append(msg);
    };
    right.append(copyBtn(K.text, "Copy all"), copyBtn(K.json, "Copy JSON"), exp);
    g.append(h("div", "gh", `<span class="label">Your kit</span>`));
    $(".gh", g).append(right);
    if (st.note) g.append(h("div", "gd pk-note", esc(st.note)));
    if (st.problems.length) g.append(h("div", "pk-err pk-problems", "This kit doesn't fully match blaze.postkit/1:<br>" + st.problems.map(esc).join("<br>")));
    const top = h("div", "box pad pk-top");
    top.append(field("Hook", k.hook, `${(k.hook || "").trim().split(/\s+/).filter(Boolean).length} / 12 words`));
    top.append(field("Hashtags", (k.hashtags || []).join(" ")));
    top.append(field("Alt text", k.alt_text, `${cps(k.alt_text)} / 125`));
    g.append(top);
    const plats = (OPT && OPT.platforms) || [];
    const order = plats.map(p => p.id).filter(id => k.platforms && k.platforms[id]);
    Object.keys(k.platforms || {}).forEach(id => { if (!order.includes(id)) order.push(id); });
    const cards = h("div", "pk-cards");
    order.forEach(id => {
      const p = plats.find(x => x.id === id) || { id, name: id, limit: 0 };
      const post = k.platforms[id];
      const n = id === "x" ? xlen(post.caption) : cps(post.caption);
      const c = h("div", "box pk-card");
      c.dataset.id = id;
      const head = h("div", "pk-ch", `<b>${esc(p.name)}</b><span class="pk-count ${p.limit && n > p.limit ? "over" : ""}">${n}${p.limit ? " / " + p.limit : ""}</span>`);
      head.append(copyBtn(post.title ? post.title + "\n\n" + post.caption : post.caption));
      c.append(head);
      if (post.title) c.append(h("div", "pk-title", esc(post.title)));
      c.append(h("div", "pk-cap", esc(post.caption)));
      if (post.description) { const d = h("div", "pk-desc", `<span class="label">Description</span>${esc(post.description)}`); d.append(copyBtn(post.description)); c.append(d); }
      cards.append(c);
    });
    g.append(cards);
    g.append(cropsBox(k, K.thumb));
    el.append(g);
  }

  // The frames over the photo (or the planned 1080x1920 shot for an idea).
  function cropsBox(k, thumb) {
    const s = k.source || {};
    const box = h("div", "box pad pk-crops");
    box.append(h("div", "pk-fh", `<span class="label">${thumb ? "Crops" : "Shot guide"}</span><span class="pk-count">${thumb ? `${s.width} × ${s.height}` : `frames for a ${s.width || 1080} × ${s.height || 1920} shot`}</span>`));
    const row = h("div", "pk-frames");
    (k.crops || []).forEach(c => {
      const f = h("div", "pk-frame");
      f.dataset.ratio = c.ratio;
      const [rw, rh] = c.ratio.split(":").map(Number);
      const view = h("div", "pk-fv"); view.style.aspectRatio = `${rw} / ${rh}`;
      if (thumb && s.width && s.height) {
        const img = h("img"); img.alt = ""; img.src = thumb;
        img.style.width = (s.width / c.w * 100) + "%";
        img.style.left = (-c.x / c.w * 100) + "%";
        img.style.top = (-c.y / c.h * 100) + "%";
        view.append(img);
      }
      f.append(view, h("div", "pk-fl", `${esc(c.ratio)}${c.file ? " · " + esc(c.file) : ""}`));
      row.append(f);
    });
    box.append(row);
    return box;
  }

  window.VIEWS = window.VIEWS || {};
  window.VIEWS.postkit = postkitView;
  if (window.BlazeViews && !BlazeViews.find("postkit")) BlazeViews.LIST.splice(BlazeViews.LIST.length - 2, 0, { id: "postkit", name: "Post kit", icon: "sparks", desc: "Creators: a photo or an idea becomes captions, hashtags, alt text and crops", open: () => show("postkit") });
  if (typeof COMMANDS !== "undefined") COMMANDS.push({ name: "postkit", desc: "Post kit: captions, hashtags, alt text and crops from a photo or an idea", run: () => show("postkit") });
  window.PostKit = { state: st, xlen };
})();
