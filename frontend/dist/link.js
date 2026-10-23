// Blaze Link (v0.14): the Phone view. Pair Blaze for Android with this PC by QR code,
// see which phones are paired and online, unpair. The server side is pair.go; the phone
// side is Blaze Android's link/DesktopLink.kt. Opened from /views › Phone, /phone, or
// open_view phone.
const BlazeLink = (() => {
  let timer = null, pairing = null;

  function phoneView(el) {
    const { col } = page(el, "Blaze Link", "Pair your phone",
      "Link Blaze on Android to this PC. From the phone you can ask this PC to do things, and Blaze here can text, call and check your phone, always with your approval there. Same Wi-Fi, or anywhere when both are on Tailscale.");
    const body = h("div", "lnk"); col.append(body);
    const draw = async () => {
      if (view !== "phone") { clearInterval(timer); timer = null; return; }
      let st = {};
      try { st = await API().PairStatus() || {}; } catch (e) {}
      body.innerHTML = "";
      if (st.error) body.append(h("p", "lnk-err", esc(st.error)));

      const devs = st.devices || [];
      const g1 = group("01 · Paired phones", "", h("span", "count", String(devs.length)));
      const rows = h("div", "lnk-rows");
      devs.forEach((d, i) => {
        const r = h("div", "lnk-row", `<span class="label">${pad(i + 1)}</span><div class="main"><div class="t">${esc(d.name)} <span class="pill ${d.connected ? "acc" : ""}">${d.connected ? "Online" : "Not connected"}</span></div><div class="s">Paired ${esc(d.paired_at)}</div></div>`);
        const rm = h("button", "btn ghost xs", "Unpair");
        rm.onclick = async () => { await API().Unpair(d.id); draw(); };
        r.append(rm); rows.append(r);
      });
      if (!devs.length) rows.append(h("div", "empty", "<b>No phone paired yet.</b>Show a QR code below and scan it from the phone."));
      g1.append(rows);

      const g2 = group("02 · Pair a phone", "");
      if (pairing && !pairing.error && pairing.expires * 1000 > Date.now()) {
        const left = Math.max(0, Math.round((pairing.expires * 1000 - Date.now()) / 1000));
        g2.append(h("p", "lnk-step", "On your phone: <b>Blaze › Settings › Blaze Desktop › Scan QR code</b>."));
        const img = h("img", "lnk-qr"); img.src = pairing.qr; img.alt = "Blaze Link pairing QR code";
        g2.append(img, h("p", "label lnk-meta", `Expires in ${Math.floor(left / 60)}:${pad(left % 60)} · ${esc((pairing.addresses || []).join(", "))} · port ${esc(String(st.port || 7377))}`));
        const row = h("div", "lnk-acts");
        const cp = h("button", "btn line", "Copy pairing link");
        cp.onclick = async () => { try { await navigator.clipboard.writeText(pairing.link); cp.textContent = "Copied"; } catch (e) {} setTimeout(() => cp.textContent = "Copy pairing link", 1200); };
        const cancel = h("button", "btn ghost", "Cancel");
        cancel.onclick = async () => { await API().StopPairing(); pairing = null; draw(); };
        row.append(cp, cancel); g2.append(row);
      } else {
        if (pairing && pairing.error) g2.append(h("p", "lnk-err", esc(pairing.error)));
        g2.append(h("p", "lnk-step", "The code works once and lasts 5 minutes. The phone pins this PC's certificate, so nothing else on the network can pose as it."));
        const go = h("button", "btn accent", `${icon("phone", "i sm")}Show pairing QR code`);
        go.onclick = async () => { pairing = await API().StartPairing(); draw(); };
        g2.append(h("div", "lnk-acts")); g2.lastChild.append(go);
      }

      const g3 = group("03 · How it works", "");
      g3.append(h("ol", "lnk-how", [
        "Phone → PC: ask Blaze on the phone to do something here (files, code, installs). Approvals pop up on the phone.",
        "PC → phone: Blaze here uses <b>phone_ask</b> for texts, WhatsApp, calls, contacts and alarms. Every send needs your yes on the phone.",
        "Direct HTTPS on port 7377. No server in the middle, nothing to pay for.",
      ].map(t => `<li>${t}</li>`).join("")));
      body.append(g1, g2, g3);
    };
    clearInterval(timer); timer = setInterval(draw, 3000);
    draw();
  }

  window.VIEWS = window.VIEWS || {};
  window.VIEWS.phone = phoneView;
  COMMANDS.push({ name: "phone", desc: "Blaze Link: pair your Android phone with this PC", run: () => show("phone") });
  if (window.runtime) on("paired", name => { if (typeof toast === "function") toast("Paired with " + name); if (view === "phone") show("phone"); });
  return { phoneView };
})();
window.BlazeLink = BlazeLink;
