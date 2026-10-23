// 0.19.16 Settings › About: what Blaze is, who makes it (Blazeinchue) and which version this is.
(() => {
  if (typeof SECTIONS === "undefined") return;
  if (!SECTIONS.some(s => s[0] === "about")) SECTIONS.push(["about", "About", "sparks"]);
  window.SETTINGS_FNS = window.SETTINGS_FNS || {};
  window.SETTINGS_FNS.about = async col => {
    const v = (typeof S !== "undefined" && S.version) || "";
    const box = h("div", "about");
    box.innerHTML = `<p class="lead">Blaze for your computer: an assistant that does things on your own devices.</p>
      <div class="group"><div class="row"><span class="label">Made by</span><span class="about-maker">Blazeinchue</span></div>
      <div class="row"><span class="label">Version</span><span class="about-ver mono">${esc(v)}</span></div>
      <div class="row"><span class="label">Website</span><button class="btn ghost xs about-site">blazeinchue.pages.dev</button></div></div>
      <p class="weak small">© 2026 Blazeinchue. Blaze Desktop, Blaze CLI and Blaze Android share one Blaze account.</p>`;
    const b = box.querySelector(".about-site");
    if (b) b.onclick = () => { try { API().OpenURL("https://blazeinchue.pages.dev"); } catch (e) {} };
    col.append(box);
  };
})();
