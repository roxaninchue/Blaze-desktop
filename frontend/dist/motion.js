// Motion tokens (Motion.kt): BlazeMotion.enabled is false when the OS asks for reduced motion or the user turned on
// Settings › Reduce motion. BlazeMotion.sync() re-reads both (the Settings toggle calls it) and fires a
// "blazemotion" event on window; every animation (emblems, stream drop + shimmer, edge particles, progress bars)
// listens for it. CSS animations follow the html.reduce-motion class.
(() => {
  const reduceQuery = matchMedia('(prefers-reduced-motion: reduce)');
  const getPref = () => localStorage.getItem('blaze.reduceMotion') === '1';
  function syncMotion() {
    const forced = !!getPref();
    const enabled = !(reduceQuery.matches || forced);
    const was = window.BlazeMotion && window.BlazeMotion.enabled;
    window.BlazeMotion = window.BlazeMotion || {};
    window.BlazeMotion.enabled = enabled;
    window.BlazeMotion.sync = syncMotion;
    // A drop-in for matchMedia('(prefers-reduced-motion: reduce)') that also follows the Settings toggle.
    window.BlazeMotion.query = window.BlazeMotion.query || { get matches() { return !window.BlazeMotion.enabled; }, addEventListener(t, f) { window.addEventListener('blazemotion', f); } };
    document.documentElement.classList.toggle('reduce-motion', !enabled);
    document.documentElement.classList.toggle('motion-off', !enabled);
    document.documentElement.style.setProperty('--motion-enabled', enabled ? '1' : '0');
    if (was !== enabled) window.dispatchEvent(new CustomEvent('blazemotion', { detail: { enabled } }));
    return enabled;
  }
  reduceQuery.addEventListener?.('change', syncMotion);
  window.addEventListener('storage', e => { if (e.key === 'blaze.reduceMotion') syncMotion(); });
  syncMotion();
})();
