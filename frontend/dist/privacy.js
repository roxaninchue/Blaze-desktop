// 0.19.16 privacy for the chat chrome (the same rules as Go's internal/privacy).
// Blaze is one assistant to the user: footers, the model pill, error cards, fallback notices,
// status lines, working pills and "What happened" never name the provider or model behind an
// answer, and no person is named as Blaze's maker (Blazeinchue is). Only an explicit developer
// flag shows them: localStorage "blaze.dev" = "1" (or window.BLAZE_DEV = true).
// The reply text itself is never touched. "Google" alone is a connector and stays.
// Runs in the browser (window.PRIVACY) and under node (module.exports) for ci/privacy.test.js.
(function (root) {
  "use strict";
  const NAMES = ["groq", "cerebras", "sambanova", "cohere", "mistral", "mixtral", "codestral", "gemini", "gemma", "google ai", "google/",
    "openrouter", "nvidia", "nim", "z.ai", "zhipu", "glm", "llama", "meta-llama", "qwen", "deepseek", "gpt", "openai",
    "anthropic", "claude", "kimi", "moonshot", "huggingface", "hugging face", "together.ai", "fireworks", "deepinfra",
    "ollama", "pollinations", "phi-", "command-r", "grok", "xai", "x.ai", "perplexity", "sonar", "minimax", "nemotron"];
  const q = s => s.replace(/[.*+?^${}()|[\]\\/]/g, "\\$&");
  const alt = NAMES.slice().sort((a, b) => b.length - a.length)
    .map(n => /[/-]$/.test(n) ? q(n) + "[\\w.:/+-]*" : q(n) + "(?:\\d[\\w.:/+-]*|[-_/:.][\\w.:/+-]*)?").join("|");
  const RE = () => new RegExp("(?:^|\\b)(?:" + alt + ")\\b", "gi");
  const OWNER = /\b(?:luke|lackson|kapapa\w*)\b/i;
  const MAKER = "Blazeinchue";

  function dev() {
    try { if (root && root.BLAZE_DEV === true) return true; } catch (e) {}
    try { return typeof localStorage !== "undefined" && localStorage.getItem("blaze.dev") === "1"; } catch (e) { return false; }
  }
  const leaks = s => RE().test(String(s || ""));
  const leaksOwner = s => OWNER.test(String(s || ""));
  function scrub(s) {
    s = String(s == null ? "" : s);
    if (dev() || !leaks(s)) return s;
    return s.replace(RE(), "the model").replace(/\b(the model)(?:[\s,/·|-]+the model\b)+/gi, "$1")
      .replace(/\(the model\)/g, "").replace(/\s{2,}/g, " ").trim();
  }
  // The footer / model pill: kept when clean, else "Blaze".
  const route = s => (!dev() && leaks(s)) ? "Blaze" : String(s || "");
  // "X didn't answer, so Y did": never shown.
  const note = s => (!dev() && leaks(s)) ? "" : String(s || "");

  const api = { NAMES, MAKER, dev, leaks, leaksOwner, scrub, route, note };
  if (typeof module === "object" && module.exports) module.exports = api;
  if (root) root.PRIVACY = api;
})(typeof window !== "undefined" ? window : null);
