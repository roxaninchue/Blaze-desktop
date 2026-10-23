// One mind (v0.16): the Archive view. Chats from before one mind stay readable here,
// read-only; their words are also in the journal, so search_chat finds them.
(() => {
  async function archiveView(el, id) {
    const { col, acts } = page(el, "Archive", id ? "Archived chat" : "Old chats",
      id ? "" : "Separate chats from before Blaze had one continuous conversation. Read-only; Blaze can still find anything in them with search_chat.");
    if (id) {
      const back = h("button", "btn ghost", "All archived chats"); back.onclick = () => show("archive"); acts.append(back);
      const c = await API().ReadArchived(id);
      const box = h("div", "arch-log");
      $(".phead h1", col).textContent = c.title || "Archived chat";
      (c.messages || []).forEach(m => {
        if (m.role === "user") box.append(h("div", "arch-u", esc(m.content || "")));
        else if (m.role === "assistant" && (m.content || "").trim()) box.append(h("div", "arch-b md", MD.render(m.content)));
      });
      if (!box.children.length) box.append(h("div", "empty", "This chat has no messages."));
      col.append(box);
      return;
    }
    const list = (API().ArchivedConversations ? await API().ArchivedConversations() : []) || [];
    const g = group("Chats", "", h("span", "count", String(list.length)));
    const rows = h("div", "arch-rows");
    list.forEach((c, i) => {
      const r = h("button", "arch-row", `<span class="label">${pad(i + 1)}</span><span class="t">${esc(c.title || "Untitled")}</span><span class="s">${esc(c.updated || "")}</span>`);
      r.onclick = () => show("archive", c.id);
      rows.append(r);
    });
    if (!list.length) rows.append(h("div", "empty", "<b>Nothing archived.</b>Everything is in your one conversation."));
    g.append(rows); col.append(g);
  }
  window.VIEWS = window.VIEWS || {};
  window.VIEWS.archive = archiveView;
})();
