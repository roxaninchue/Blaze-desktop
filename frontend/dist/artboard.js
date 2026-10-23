// v0.9.0 "design anything": New design (a scratch design on a fixed artboard: post,
// story, slide, poster, app screen, email, icon, banner), the artboard drawn at its real
// size scaled to fit, and Export to PNG (2x) or PDF with the computer's Chrome or Edge.
(() => {
  const call = (fn, ...a) => (API()[fn] ? API()[fn](...a) : Promise.resolve(null));
  const note = (t, kind) => (typeof toast === "function" ? toast(t, kind) : toastLine(t));
  let formats = null;
  const getFormats = async () => formats || (formats = (await call("DesignFormats")) || []);

  // ---------- toolbar ----------
  const _designView = designView;
  designView = function (el) {
    _designView(el);
    const bar = $(".dbar", el); if (!bar) return;
    const nb = h("button", "btn line sm", `${icon("plus", "i sm")}<span class="btxt">New</span>`);
    nb.id = "dnew"; nb.title = "New design: a post, slide, poster, app screen, email or icon"; nb.dataset.pop = "1";
    nb.onclick = e => { e.stopPropagation(); newPop(nb); };
    bar.insertBefore(nb, bar.firstChild);
    const ex = h("button", "btn ghost sm", `${icon("dl", "i sm")}<span class="btxt">Export</span>`);
    ex.id = "dexport"; ex.title = "Export this design as PNG or PDF"; ex.dataset.pop = "1"; ex.style.display = "none";
    ex.onclick = e => { e.stopPropagation(); exportPop(ex); };
    bar.insertBefore(ex, $("#dopen", bar));
    // A new design asked for in the chat ("make me a poster") can start here too.
    window.BLAZE_NEW_DESIGN = (id, name) => startDesign(id, name);
  };

  async function newPop(anchor) {
    if (popEl && popEl.dataset.kind === "new") { closePop(); return; }
    const list = await getFormats();
    openPop(anchor, p => {
      p.dataset.kind = "new";
      p.innerHTML = `<div class="phd"><span class="label">New design</span></div><div class="pbody"></div>`;
      const body = $(".pbody", p);
      const name = h("input", "field abname"); name.placeholder = "Name (optional)";
      body.append(name);
      list.forEach(f => {
        const o = h("button", "opt abfmt", `<span class="abshape" style="aspect-ratio:${f.w}/${f.h}"></span><span class="col2"><span class="om" style="color:var(--ink)">${esc(f.name)} <span class="mono small sub">${f.w}×${f.h}</span></span><span class="od">${esc(f.hint)}</span></span>`);
        o.dataset.id = f.id;
        o.onclick = () => { closePop(); startDesign(f.id, name.value); };
        body.append(o);
      });
    }, { align: "left", width: 340 });
  }

  async function startDesign(id, name) {
    const v = await call("NewDesign", id, name || "");
    if (!v) return;
    D = v; viewing = 0; compare = [];
    if (typeof refreshState === "function") { try { await refreshState(); } catch (e) {} }
    drawDesign();
    const f = v.artboard;
    note(f ? `New ${f.name} (${f.w}×${f.h}). Describe it in the chat.` : "New design ready.", "ok");
    const inp = $("#input"); if (inp) inp.focus();
  }

  function exportPop(anchor) {
    if (popEl && popEl.dataset.kind === "export") { closePop(); return; }
    const f = D && D.artboard; if (!f) return;
    openPop(anchor, p => {
      p.dataset.kind = "export";
      p.innerHTML = `<div class="phd"><span class="label">Export</span><span class="grow"></span><span class="mono small sub">${f.w}×${f.h}</span></div><div class="pbody"></div>`;
      const body = $(".pbody", p);
      [["png", "PNG", `Image at 2×, ${f.w * 2}×${f.h * 2} px`], ["pdf", "PDF", f.print ? `Print-ready ${esc(f.print)}` : "Vector text, for sharing or print"]].forEach(([k, l, d]) => {
        const o = h("button", "opt", `<span class="on-mark">${icon("dl", "i sm")}</span><span class="col2"><span class="om" style="color:var(--ink)">${l}</span><span class="od">${d}</span></span>`);
        o.onclick = async () => {
          closePop(); note(`Exporting ${l}…`);
          const r = await call("ExportDesign", k);
          if (!r || r.error) { note((r && r.error) || "Export failed", "error"); return; }
          note(`Saved ${r.path.split(/[\\/]/).slice(-2).join("/")}`, "ok");
          call("RevealFile", r.path);
        };
        body.append(o);
      });
    }, { width: 280 });
  }

  // ---------- the stage: an artboard at its real size, scaled to fit ----------
  const _drawStage = drawStage;
  drawStage = function () {
    _drawStage();
    const f = D && D.artboard;
    const ex = $("#dexport"); if (ex) ex.style.display = f ? "" : "none";
    const dev = $("#ddev"); if (dev) dev.style.display = f ? "none" : "";
    const st = $("#dstage"); if (!st) return;
    const empty = $(".dempty .keyrow", st);
    if (empty && !$(".abnew", empty)) {
      const b = h("button", "btn line sm abnew", `${icon("plus", "i sm")}New design…`);
      b.onclick = e => { e.stopPropagation(); newPop($("#dnew") || b); };
      empty.append(b);
    }
    if (!f) { st.classList.remove("artboard"); return; }
    st.classList.add("artboard");
    fit();
  };

  function fit() {
    const f = D && D.artboard, st = $("#dstage"); if (!f || !st) return;
    const boxes = $$(".frames > .fbox", st); if (!boxes.length) return;
    const frames = $(".frames", st);
    const cs = getComputedStyle(frames);
    const padX = parseFloat(cs.paddingLeft) + parseFloat(cs.paddingRight), padY = parseFloat(cs.paddingTop) + parseFloat(cs.paddingBottom);
    const gap = parseFloat(cs.columnGap || cs.gap) || 16;
    const availW = (frames.clientWidth - padX - gap * (boxes.length - 1)) / boxes.length;
    const capH = boxes.some(b => $(".fcap", b)) ? 22 : 0;
    const availH = frames.clientHeight - padY - capH;
    const s = Math.max(0.05, Math.min(availW / f.w, availH / f.h, 1));
    boxes.forEach(b => {
      b.classList.add("abox");
      b.style.flex = "none"; b.style.width = Math.floor(f.w * s) + "px";
      let holder = $(".abholder", b);
      $$("iframe.pframe", b).forEach(fr => {
        if (!holder) { holder = h("div", "abholder"); fr.replaceWith(holder); }
        if (fr.parentNode !== holder) holder.append(fr);
        fr.style.width = f.w + "px"; fr.style.height = f.h + "px"; fr.style.flex = "none";
        fr.style.transform = `scale(${s})`; fr.style.transformOrigin = "0 0";
      });
      if (holder) { holder.style.width = Math.floor(f.w * s) + "px"; holder.style.height = Math.floor(f.h * s) + "px"; }
    });
    const lab = $(".abscale", st) || h("div", "label abscale");
    lab.textContent = `${f.name} · ${f.w}×${f.h} · ${Math.round(s * 100)}%`;
    if (!lab.isConnected) st.append(lab);
  }

  let ro = null;
  const watch = () => {
    const st = $("#dstage");
    if (!st || (ro && ro.el === st) || typeof ResizeObserver === "undefined") return;
    if (ro) ro.disconnect();
    ro = new ResizeObserver(() => { clearTimeout(watch.t); watch.t = setTimeout(fit, 60); }); ro.el = st; ro.observe(st);
  };
  setInterval(watch, 1000);
})();
