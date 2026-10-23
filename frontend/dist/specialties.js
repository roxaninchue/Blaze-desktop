// Settings › Account › Specialties (specialties_app.go, gateway 0.8.45 GET/DELETE /v1/specialties).
// What Blaze has learned for each kind of task on this Blaze account (steps plus the user's own
// rules), shared with Blaze on every device. The user can open one, delete it, delete a single
// rule, or clear the general rules. Loaded after cloudsync.js; wraps SETTINGS_FNS.account the same
// way, so the rest of Settings is untouched. Uses app.js globals (API, $, h, esc, group, toast).
(() => {
  const base = window.SETTINGS_FNS && window.SETTINGS_FNS.account;
  if (!base) return;

  window.SETTINGS_FNS.account = async (col, redraw) => {
    await base(col, redraw);
    if (typeof ACCT === "undefined" || !ACCT || !ACCT.signed_in) return;
    const danger = [...col.querySelectorAll(".group")].find(x => x.querySelector(".dangerrow"));
    col.insertBefore(specGroup(), danger || null);
  };

  const day = s => { const d = new Date(s); return isNaN(d) ? "" : d.toLocaleDateString("en-GB", { day: "numeric", month: "short" }); };
  const plural = (n, w) => `${n} ${w}${n === 1 ? "" : "s"}`;

  function specGroup() {
    const g = group("Specialties", "What Blaze has learned for the kinds of tasks you ask for often: the steps that work and the rules you gave it. They're kept on your Blaze account, so Blaze on your phone and your other computers follows them too.");
    const bx = h("div", "box pad specbox");
    const st = h("div", "out small sub", "Loading…");
    const list = h("div", "speclist");
    bx.append(st, list);
    g.append(bx);

    let open = null; // id of the opened specialty, "general" for the general rules

    const ruleRow = (id, text, n) => {
      const r = h("div", "keyrow specrule");
      r.append(h("span", "grow small", esc(text)));
      const del = h("button", "btn ghost xs", "Delete");
      del.title = "Delete this rule";
      del.onclick = async () => {
        if (!confirm(`Delete this rule?\n\n“${text}”\n\nBlaze stops following it on every device.`)) return;
        del.disabled = true;
        const v = await API().SpecialtyRuleDelete(id, n);
        render(v);
        if (!v.error) toast("Rule deleted.", "ok");
      };
      r.append(del);
      return r;
    };

    const detail = (v) => {
      const s = v.open, d = h("div", "specdetail");
      if (s.steps && s.steps.length) {
        d.append(h("div", "label small", "Steps"));
        const ol = h("ol", "small specsteps");
        s.steps.forEach(x => ol.append(h("li", "", esc(x))));
        d.append(ol);
      }
      d.append(h("div", "label small", "Your rules"));
      if (!s.rules || !s.rules.length) d.append(h("div", "small sub", "No rules yet. Tell Blaze “always …” or “never …” and it adds one."));
      else s.rules.forEach((x, n) => d.append(ruleRow(s.id, x, n)));
      return d;
    };

    const row = (id, name, sub, v) => {
      const el = h("div", "specrow" + (open === id ? " open" : ""));
      const line = h("div", "keyrow");
      const btn = h("button", "btn line sm grow", `<span>${esc(name)}</span>`);
      btn.style.justifyContent = "flex-start";
      btn.title = open === id ? "Hide" : "Show the steps and rules";
      btn.onclick = async () => {
        if (open === id) { open = null; render(v); return; }
        open = id; btn.disabled = true;
        render(await API().SpecialtyGet(id));
      };
      const meta = h("span", "small sub", esc(sub));
      const del = h("button", "btn ghost xs", id === "general" ? "Clear" : "Delete");
      del.onclick = async () => {
        const msg = id === "general" ? "Clear all your general rules? Blaze stops following them on every device." : `Delete the “${name}” specialty and its rules? Blaze stops using it on every device. It can come back if you ask for this kind of task again.`;
        if (!confirm(msg)) return;
        del.disabled = true;
        if (open === id) open = null;
        const nv = await API().SpecialtyDelete(id);
        render(nv);
        if (!nv.error) toast(id === "general" ? "General rules cleared." : "Specialty deleted.", "ok");
      };
      line.append(btn, meta, del);
      el.append(line);
      if (open === id && v.open && v.open.id === id) el.append(detail(v));
      return el;
    };

    function render(v) {
      if (!v) return;
      list.innerHTML = "";
      if (v.open) open = v.open.id;
      const items = v.items || [], gen = v.generalRules || [];
      if (v.error) { st.className = "out small bad"; st.textContent = v.error; }
      else if (!items.length && !gen.length) { st.className = "out small sub"; st.textContent = "None yet. When you ask for the same kind of task again, Blaze saves what worked here."; }
      else { st.className = "out small sub"; st.textContent = ""; }
      if (!v.signedIn) return;
      items.forEach(s => list.append(row(s.id, s.name, [plural(s.rules || 0, "rule"), s.updated_at ? "updated " + day(s.updated_at) : ""].filter(Boolean).join(" · "), v)));
      if (gen.length) list.append(row("general", "General rules", plural(gen.length, "rule"), v));
    }

    API().SpecialtiesList().then(render).catch(() => { st.className = "out small bad"; st.textContent = "Couldn't load your specialties right now."; });
    return g;
  }
})();
