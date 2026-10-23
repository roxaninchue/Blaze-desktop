// v0.18.1 BrandProgress (BlazeGlass.kt): a 3 px rounded bar in brand colours with a white shimmer band sweeping
// across it every 1100 ms while it loads. pct < 0 = indeterminate. Static under reduced motion (CSS: html.reduce-motion).
// Used for the self-update download and the Whisper / voice-model download. .skeleton placeholders live in motion.css.
const BrandProgress = (() => {
  function create(pct = -1, cls = "") {
    const el = document.createElement("div");
    el.className = "bprog" + (cls ? " " + cls : "");
    el.setAttribute("role", "progressbar");
    el.setAttribute("aria-valuemin", "0"); el.setAttribute("aria-valuemax", "100");
    el.innerHTML = "<i></i>";
    set(el, pct);
    return el;
  }
  function set(el, pct) {
    if (!el) return;
    const ind = !(pct >= 0);
    el.classList.toggle("ind", ind);
    const fill = el.querySelector("i");
    if (fill) fill.style.width = ind ? "" : Math.max(4, Math.min(100, pct)) + "%";
    if (ind) el.removeAttribute("aria-valuenow"); else el.setAttribute("aria-valuenow", String(Math.round(pct)));
  }
  // A few shimmering lines where a "Loading…" placeholder used to be.
  function skeleton(lines = 3) {
    return `<div class="skeleton-stack" role="status" aria-label="Loading">${Array.from({ length: lines }, (_, i) => `<span class="skeleton sk-line" style="width:${[92, 74, 58, 84][i % 4]}%"></span>`).join("")}</div>`;
  }
  return { create, set, skeleton };
})();
window.BrandProgress = BrandProgress;
