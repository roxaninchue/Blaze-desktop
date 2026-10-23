// The usage meter (v0.19.9): Blaze Cloud messages left today, the same numbers as Blaze CLI (gateway
// 0.7.0). A small chip in the top bar: plain while there's room, amber at 20% or less, red when
// empty. Click for the details (messages, tokens, reset time, the last reply) and your own daily
// cap. It refreshes after every reply; it never spends a message to check.
(() => {
  let U = null, warned = "";
  const pct = q => q && q.requests_limit > 0 ? q.requests_left / q.requests_limit : 1;
  const when = s => { const d = new Date(s); return isNaN(d) ? (s || "") : d.toLocaleString([], { weekday: "short", hour: "numeric", minute: "2-digit" }); };
  const n = v => Number(v || 0).toLocaleString();

  function chip() {
    let b = document.getElementById("usageChip");
    const q = U && U.quota;
    if (!U || !U.cloud || !U.signed_in || !q) { if (b) b.remove(); return; }
    if (!b) {
      b = h("button", "chip usagechip"); b.id = "usageChip"; b.title = "Blaze Cloud usage today";
      b.onclick = e => { e.stopPropagation(); pop(b); };
      const at = document.getElementById("updPill") || document.getElementById("routerBtn");
      if (at) at.before(b); else { const t = document.getElementById("top"); if (t) t.append(b); }
    }
    const p = pct(q);
    b.dataset.level = q.requests_left <= 0 ? "out" : p <= 0.2 ? "low" : "ok";
    b.textContent = q.requests_limit > 0 ? `${n(q.requests_left)} left` : "Unlimited";
    // A heads-up once a day when it gets low, before it runs out.
    const day = (q.resets_at || "").slice(0, 10);
    if (p <= 0.1 && q.requests_left > 0 && warned !== day && typeof toast === "function") { warned = day; toast(`${n(q.requests_left)} Blaze Cloud messages left today`, "warn"); }
  }

  function bar(used, limit) {
    if (!(limit > 0)) return `<div class="ubar none"></div>`;
    const w = Math.min(100, Math.round(used / limit * 100));
    return `<div class="ubar"><span style="width:${w}%"></span></div>`;
  }

  function pop(anchor) {
    const old = document.querySelector(".usagepop"); if (old) { old.remove(); return; }
    const q = U.quota, box = h("div", "pop usagepop");
    box.innerHTML = `<div class="label acc">Blaze Cloud today${q.plan ? ` · ${esc(q.plan[0].toUpperCase() + q.plan.slice(1))}` : ""}</div>
      <div class="urow"><span>Messages</span><span>${n(q.requests_used)} of ${q.requests_limit > 0 ? n(q.requests_limit) : "unlimited"}</span></div>${bar(q.requests_used, q.requests_limit)}
      <div class="urow"><span>Tokens</span><span>${q.tokens_limit > 0 ? `${n(q.tokens_used)} of ${n(q.tokens_limit)}` : `${n(q.tokens_used)} · no cap`}</span></div>${bar(q.tokens_used, q.tokens_limit)}
      ${q.last_reply_tokens ? `<div class="urow sub"><span>Last reply</span><span>${n(q.last_reply_tokens)} tokens</span></div>` : ""}
      <div class="urow sub"><span>Resets</span><span>${esc(when(q.resets_at))}</span></div>
      <form class="ucap"><label class="small sub">Your daily cap <input class="field" type="number" min="0" step="1" value="${U.cap || ""}" placeholder="none"> messages</label><button class="btn line xs">Save</button></form>
      <div class="small weak">Checking this is free. The cap stops Blaze before it goes over.</div>`;
    box.querySelector(".ucap").onsubmit = async e => {
      e.preventDefault();
      const v = parseInt(box.querySelector(".ucap input").value || "0", 10) || 0;
      U = await API().SetCloudDailyCap(v); chip(); box.remove();
      if (typeof toast === "function") toast(v ? `Cap set: ${v} messages a day` : "Cap removed", "ok");
    };
    document.body.append(box);
    const r = anchor.getBoundingClientRect();
    box.style.position = "fixed"; box.style.top = (r.bottom + 6) + "px"; box.style.right = Math.max(8, innerWidth - r.right) + "px";
    setTimeout(() => addEventListener("click", function off(ev) { if (!box.contains(ev.target)) { box.remove(); removeEventListener("click", off); } }), 0);
  }

  async function refresh(force) {
    try { if (API().CloudUsage) { U = await API().CloudUsage(!!force); chip(); } } catch (e) { /* the meter is optional */ }
  }
  on("done", () => refresh(false));
  on("error", () => refresh(false));
  on("account", () => refresh(true));
  setTimeout(() => refresh(true), 2500);
  window.UsageMeter = { refresh, get state() { return U; } };
})();
