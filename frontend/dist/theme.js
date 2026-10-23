// Light/dark always one click away: a sun/moon button in the top bar (and the orb bar).
// Click flips between light and dark; right-click goes back to following the system.
// Settings › Appearance keeps the full System / Light / Dark choice.
const Theme = (() => {
  const SUN = `<svg class="i sm" viewBox="0 0 16 16"><circle cx="8" cy="8" r="3"/><path d="M8 1.5v1.8M8 12.7v1.8M1.5 8h1.8M12.7 8h1.8M3.4 3.4l1.3 1.3M11.3 11.3l1.3 1.3M3.4 12.6l1.3-1.3M11.3 4.7l1.3-1.3"/></svg>`;
  const MOON = `<svg class="i sm" viewBox="0 0 16 16"><path d="M13.5 9.8A5.8 5.8 0 0 1 6.2 2.5a5.8 5.8 0 1 0 7.3 7.3z"/></svg>`;
  const isDark = () => document.documentElement.dataset.theme === "dark";

  function paint() {
    const dark = isDark(), sys = !S || !S.prefs || (S.prefs.theme || "system") === "system";
    const tip = `${dark ? "Switch to light" : "Switch to dark"}${sys ? " · following your system" : " · right-click to follow your system"}`;
    document.querySelectorAll(".themebtn").forEach(b => {
      b.innerHTML = dark ? MOON : SUN;
      b.title = tip; b.setAttribute("aria-label", tip);
      b.classList.toggle("sys", sys);
    });
  }

  async function set(t) {
    if (S && S.prefs) S.prefs.theme = t;
    document.documentElement.classList.add("themefade");
    applyTheme();
    setTimeout(() => document.documentElement.classList.remove("themefade"), 320);
    try { await API().SetTheme(t); } catch (e) {}
    if (view === "settings" && typeof settingsSection !== "undefined" && settingsSection === "appearance") show("settings", "appearance");
  }
  function setMotion(on) {
    localStorage.setItem('blaze.reduceMotion', on ? '0' : '1');
    if (window.BlazeMotion && typeof window.BlazeMotion.sync === 'function') window.BlazeMotion.sync();
    else if (window.BlazeMotion) window.BlazeMotion.enabled = !on;
    const onEl = document.querySelector('#reduceMotionToggle');
    if (onEl) onEl.checked = !on;
  }
  function wire(b) {
    b.onclick = e => { e.stopPropagation(); set(isDark() ? "light" : "dark"); };
    b.oncontextmenu = e => { e.preventDefault(); set("system"); };
    b.style.setProperty("--wails-draggable", "no-drag");
  }

  // Every applyTheme (boot, Settings, /theme, the system switching) repaints the icon.
  const base = window.applyTheme;
  window.applyTheme = function () { base(); paint(); };

  document.querySelectorAll(".themebtn").forEach(wire);
  // The orb bar is built on demand; wire its button when it appears.
  new MutationObserver(() => document.querySelectorAll(".themebtn:not([data-w])").forEach(b => { b.dataset.w = "1"; wire(b); paint(); }))
    .observe(document.body, { childList: true, subtree: true });
  paint();
  return { set, paint, setMotion };
})();
