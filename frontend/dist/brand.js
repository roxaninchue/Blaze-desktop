// Brand logos for connectors and model providers, bundled offline in icons/
// (sources and licences in icons/LICENSE.md). Shown in their own colours on a light
// square tile so dark marks (GitHub, Notion, Ollama) read in both themes.
const BRAND = {
  // connectors
  google: { name: "Google", cap: "Gmail, Calendar, Drive, Contacts" },
  github: { name: "GitHub", cap: "Repos, issues, pull requests, notifications" },
  spotify: { name: "Spotify", cap: "Now playing, playback, search" },
  notion: { name: "Notion", cap: "Search, read and add to pages" },
  slack: { name: "Slack", cap: "Channels, messages" },
  mcp: { name: "MCP server", cap: "Model Context Protocol" },
  // model providers
  gemini: { name: "Google Gemini" }, groq: { name: "Groq" }, cerebras: { name: "Cerebras" },
  openrouter: { name: "OpenRouter" }, mistral: { name: "Mistral" }, nvidia: { name: "NVIDIA NIM" },
  deepseek: { name: "DeepSeek" }, moonshot: { name: "Kimi" }, openai: { name: "OpenAI" },
  anthropic: { name: "Anthropic" }, xai: { name: "xAI" }, ollama: { name: "Ollama" },
};
// No public logo: a custom OpenAI-compatible endpoint and local MCP commands use Blaze line marks.
const BRAND_LINE = {
  custom: '<path d="M2.5 4.5h11v7h-11z"/><path d="m5 7 1.5 1L5 9M8 9.5h3"/>',
  local: '<rect x="1.5" y="3" width="13" height="10"/><path d="m4 6.5 2 1.5-2 1.5M7.5 10h4"/>',
};
function logo(id, cls = "") {
  if (BRAND[id]) return `<span class="blogo ${cls}"><img src="icons/${id}.svg" alt="" draggable="false"></span>`;
  return `<span class="blogo line ${cls}"><svg class="i" viewBox="0 0 16 16">${BRAND_LINE[id] || BRAND_LINE.custom}</svg></span>`;
}
