// Show / hide for every password and passphrase field (v0.19.7): sign-in, create account, reset,
// two-factor, keys, tokens and any field added later. A small eye button sits inside the field;
// it switches the field between dots and plain text and back. Fields opt out with data-noreveal.
(() => {
  const EYE = '<svg class="i sm" viewBox="0 0 16 16"><path d="M1.5 8S4 3.5 8 3.5 14.5 8 14.5 8 12 12.5 8 12.5 1.5 8 1.5 8z"/><circle cx="8" cy="8" r="2"/></svg>';
  const SHUT = '<svg class="i sm" viewBox="0 0 16 16"><path d="M1.5 8S4 3.5 8 3.5 14.5 8 14.5 8 12 12.5 8 12.5 1.5 8 1.5 8z"/><circle cx="8" cy="8" r="2"/><path d="m2.5 13.5 11-11"/></svg>';
  function wrap(inp) {
    if (inp.dataset.reveal || inp.hasAttribute("data-noreveal")) return;
    inp.dataset.reveal = "1";
    const box = document.createElement("span"); box.className = "pwbox";
    inp.before(box); box.append(inp);
    const b = document.createElement("button");
    b.type = "button"; b.className = "pweye"; b.tabIndex = 0;
    const set = shown => {
      inp.type = shown ? "text" : "password";
      b.innerHTML = shown ? SHUT : EYE;
      b.title = shown ? "Hide" : "Show";
      b.setAttribute("aria-label", shown ? "Hide password" : "Show password");
      b.setAttribute("aria-pressed", shown ? "true" : "false");
    };
    set(false);
    b.addEventListener("mousedown", e => e.preventDefault()); // keep the caret in the field
    b.addEventListener("click", e => { e.preventDefault(); e.stopPropagation(); set(inp.type === "password"); inp.focus(); });
    // Never leave a secret showing once the form is sent.
    const f = inp.form; if (f) f.addEventListener("submit", () => set(false), true);
    box.append(b);
  }
  const scan = root => { if (root.querySelectorAll) root.querySelectorAll('input[type="password"]').forEach(wrap); if (root.matches && root.matches('input[type="password"]')) wrap(root); };
  scan(document);
  new MutationObserver(ms => { for (const m of ms) for (const n of m.addedNodes) if (n.nodeType === 1) scan(n); }).observe(document.documentElement, { childList: true, subtree: true });
  window.PwReveal = { scan };
})();
