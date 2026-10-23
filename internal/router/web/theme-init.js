// Light/dark before the first paint: ?theme= or #theme= (Blaze passes its own), then the
// saved choice, then the system's.
(function () {
  var m = (location.hash + "&" + location.search).match(/theme=(light|dark)/);
  var t = m ? m[1] : null;
  try { if (!t) t = localStorage.getItem("blaze-theme"); } catch (e) {}
  if (!t) t = matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
  document.documentElement.dataset.theme = t;
  if (window.parent !== window) document.documentElement.classList.add("embedded");
})();
