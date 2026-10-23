// /views: every page that used to sit in the sidebar (Memory, Jobs, Schedule, Accounts,
// Doctor, Settings…) plus Design and BlazeRouter, in one picker. Nothing is deleted: the
// direct commands (/memory, /jobs, /design, /router…) and show() still work.
const BlazeViews = (() => {
  const badge = sel => () => { const e = $(sel); return e ? e.textContent.trim() : ""; };
  const LIST = [
    { id: "memory", name: "Memory", icon: "brain", desc: "What Blaze remembers: graph and timeline", open: () => show("memory") },
    { id: "jobs", name: "Jobs", icon: "sliders", desc: "Background jobs and their logs", open: () => show("jobs"), badge: badge("#jobCount") },
    { id: "schedule", name: "Schedule", icon: "clock", desc: "Timed jobs and reminders", open: () => show("schedule") },
    { id: "accounts", name: "Accounts", icon: "key", desc: "Connections, logins, API keys, MCP", open: () => show("accounts"), badge: badge("#connCount") },
    { id: "doctor", name: "Doctor", icon: "chart", desc: "Check this PC, models and tools", open: () => show("doctor") },
    { id: "settings", name: "Settings", icon: "sliders", desc: "Behaviour, appearance, memory, skills", open: () => show("settings"), badge: badge("#updCount") },
    { id: "design", name: "Design", icon: "palette", desc: "Live preview, artboards, 3D", open: () => show("design") },
    { id: "router", name: "BlazeRouter", icon: "layers", desc: "Advanced: a local gateway for your own keys and other apps", open: () => show("router") },
    { id: "archive", name: "Archive", icon: "book", desc: "Chats from before one mind (read-only)", open: () => show("archive") },
    { id: "learn", name: "Learn", icon: "book", desc: "Flashcards with spaced repetition (tutor skill)", open: () => show("learn") },
    { id: "phone", name: "Phone", icon: "phone", desc: "Blaze Link: pair Blaze for Android with this PC", open: () => show("phone") },
    { id: "skills", name: "Skills", icon: "book", desc: "Bundled and learned skills", open: () => show("settings", "skills") },
    { id: "usage", name: "Usage", icon: "chart", desc: "Tokens and cost per model", open: () => show("settings", "usage") },
  ];
  const find = id => LIST.find(v => v.id === id);
  const label = id => { const v = find(id); return v ? v.name : id === "work" ? "Chat" : id; };

  // Rows for the composer picker (app.js openMenu): icon, name, description, badge.
  const items = () => LIST.map(v => {
    const b = v.badge ? v.badge() : "";
    if (typeof CIC !== "undefined" && IC[v.icon]) CIC["v_" + v.icon] = IC[v.icon];
    return { name: v.name, icon: "v_" + v.icon, desc: v.desc + (b ? " · " + b : ""), run: () => v.open() };
  });

  // Open the picker in the composer (switching back to the chat first when needed).
  function picker(q) {
    const go = () => openMenu("sub", items(), "Views", { q: q || "" });
    if (view !== "work" && view !== "design") { show("work"); setTimeout(go, 0); } else go();
  }

  // Blaze opened a view itself (open_view tool).
  if (window.runtime) on("view:open", id => {
    if (id === "chat" || id === "work") { show("work"); return; }
    const v = find(id); if (v) v.open();
  });

  COMMANDS.splice(1, 0, { name: "views", desc: "Every page: Memory, Jobs, Schedule, Accounts, Doctor, Settings…", sub: async () => items() });
  return { LIST, items, picker, label, find };
})();
window.BlazeViews = BlazeViews;
