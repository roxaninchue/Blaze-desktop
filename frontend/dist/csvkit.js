// CSV report (W2, local half; csv_app.go + internal/csvkit). Drop or open a CSV → cleaned
// table, group summary, chart and MD/PDF export, computed on this computer. The written summary
// is a placeholder slot until a model writes it.
const CSVKit = (() => {
  let sheet = null, req = null, last = null;
  const isCSV = p => /\.(csv|tsv)$/i.test(String(p || ""));
  const E = s => String(s ?? "").replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
  const api = () => window.go && window.go.main && window.go.main.App;

  function close() { if (sheet) { sheet.remove(); sheet = null; } }

  async function run(q) {
    req = q;
    paint({ busy: true });
    const r = await api().CSVReport(q);
    last = r;
    paint(r);
  }

  async function pick() { const p = await api().CSVPick(); if (p) run({ path: p }); }

  function opts(list, cur, extra) {
    return (extra || []).concat(list).map(o => `<option value="${E(o.v)}"${o.v === cur ? " selected" : ""}>${E(o.t)}</option>`).join("");
  }

  function table(hdr, rows, right, boldLast) {
    const th = hdr.map((x, i) => `<th${right[i] ? ' class="r"' : ""}>${E(x)}</th>`).join("");
    const tr = rows.map((r, k) => `<tr${boldLast && k === rows.length - 1 ? ' class="tot"' : ""}>${r.map((c, i) => `<td${right[i] ? ' class="r"' : ""}>${E(c)}</td>`).join("")}</tr>`).join("");
    return `<table><thead><tr>${th}</tr></thead><tbody>${tr}</tbody></table>`;
  }

  function paint(res) {
    if (!sheet) {
      sheet = document.createElement("div");
      sheet.className = "csvk-wrap";
      sheet.addEventListener("click", e => { if (e.target === sheet) close(); });
      document.body.append(sheet);
    }
    if (res.busy) { sheet.innerHTML = `<div class="csvk"><div class="csvk-busy">Cleaning ${E((req.path || "").split(/[\\/]/).pop())}…</div></div>`; return; }
    if (res.error) {
      sheet.innerHTML = `<div class="csvk"><div class="csvk-h"><b>CSV report</b><button class="csvk-x" title="Close">×</button></div><div class="csvk-err">${E(res.error)}</div></div>`;
      sheet.querySelector(".csvk-x").onclick = close;
      return;
    }
    const r = res.report, s = r.summary, cols = r.columns;
    const groupCols = cols.filter(c => c.type === "category" || c.type === "date" || c.type === "text").map(c => ({ v: c.name, t: c.name }));
    const numCols = cols.filter(c => c.type === "number").map(c => ({ v: c.name, t: c.name }));
    const fmt = (v, agg) => {
      if (agg === "count") return String(Math.round(v));
      const sc = agg === "mean" ? Math.max(2, s.scale) : s.scale;
      return Number(v).toLocaleString("en-US", { minimumFractionDigits: sc, maximumFractionDigits: sc });
    };
    const ghdr = s.value ? [s.by, "Sum of " + s.value, "Count", "Mean " + s.value] : [s.by, "Rows"];
    const grow = g => s.value ? [g.key, fmt(g.sum, "sum"), String(g.count), fmt(g.mean, "mean")] : [g.key, String(g.rows)];
    const grows = s.groups.map(grow).concat([grow(s.total)]);
    const dhdr = cols.map(c => c.name), dright = cols.map(c => c.type === "number");
    const more = r.rows > r.preview.length ? `<div class="csvk-sub">First ${r.preview.length} of ${r.rows} rows. The full table is in the cleaned CSV.</div>` : "";
    const f = r.files;
    sheet.innerHTML = `<div class="csvk" role="dialog" aria-label="CSV report">
      <div class="csvk-h"><b>${E(r.name)}</b><span class="csvk-sub">${r.rows} rows · ${r.delimiter}-separated · computed on this computer</span><button class="csvk-x" title="Close">×</button></div>
      <div class="csvk-bar">
        <label>Group by <select data-k="group_by">${opts(groupCols, s.by)}</select></label>
        <label>Value <select data-k="value">${opts(numCols, s.value, [{ v: "", t: "(row count)" }])}</select></label>
        <label>Show <select data-k="agg">${opts([{ v: "sum", t: "Sum" }, { v: "count", t: "Count" }, { v: "mean", t: "Mean" }], s.agg)}</select></label>
        <label>Chart <select data-k="chart">${opts([{ v: "bar", t: "Bar" }, { v: "line", t: "Line" }], r.chart_kind)}</select></label>
      </div>
      <div class="csvk-slot" data-slot="summary"><span class="label">Summary</span><p>${r.written ? E(r.written) : "The written summary appears here once a model is available. Everything else here was computed on this computer."}</p></div>
      <div class="csvk-chart">${r.chart_svg}</div>
      <div class="csvk-sec"><span class="label">${E(s.by)}</span>${table(ghdr, grows, [false, true, true, true], true)}</div>
      <details class="csvk-sec"><summary>What cleaning did</summary><ul>${r.notes.map(n => `<li>${E(n)}</li>`).join("")}</ul></details>
      <div class="csvk-sec"><span class="label">Cleaned data</span>${more}<div class="csvk-data">${table(dhdr, r.preview, dright, false)}</div></div>
      <div class="csvk-acts">
        <button data-open="${E(f.md)}">Open Markdown</button><button data-open="${E(f.pdf)}">Open PDF</button>
        <button data-open="${E(f.cleaned)}">Open cleaned CSV</button><button class="csvk-rev">Show in folder</button>
      </div></div>`;
    sheet.querySelector(".csvk-x").onclick = close;
    sheet.querySelectorAll(".csvk-bar select").forEach(sel => sel.onchange = () => {
      const q = { path: req.path };
      sheet.querySelectorAll(".csvk-bar select").forEach(x => q[x.dataset.k] = x.value);
      if (sel.dataset.k === "group_by") q.chart = "";
      run(q);
    });
    sheet.querySelectorAll("[data-open]").forEach(b => b.onclick = async () => { const m = await api().CSVOpen(b.dataset.open); if (m && typeof toastLine === "function") toastLine(m); });
    sheet.querySelector(".csvk-rev").onclick = () => api().RevealFile(f.cleaned);
  }

  // A CSV chip in the composer gets a "Table" button.
  function decorate(chip, a) {
    if (!a || a.kind !== "file" || !isCSV(a.path)) return;
    const b = document.createElement("button");
    b.className = "csvk-chip"; b.textContent = "Table"; b.title = "Clean it and show a table, chart and report (on this computer)";
    b.onclick = e => { e.stopPropagation(); run({ path: a.path }); };
    chip.insertBefore(b, chip.querySelector(".ax"));
  }

  if (window.runtime && window.runtime.EventsOn) {
    window.runtime.EventsOn("csv:report", r => { last = r; });
  }
  addEventListener("keydown", e => { if (e.key === "Escape" && sheet) close(); });

  return { run, pick, decorate, close, isCSV, get last() { return last; } };
})();
window.CSVKit = CSVKit;
