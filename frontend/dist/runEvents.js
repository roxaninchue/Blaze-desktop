// 0.19.16 run events: the ONE place the chat reads "what is Blaze doing" and "did my message
// arrive" from. Go sends normalized "run_event"s (internal/runevents; that file is where the Site
// run-event contract plugs in); this module also derives the same events from the classic turn's
// events (tool, status, delta, done…) and from local tools, so every path looks the same to the
// working pill and the delivery status (pills.js).
//
// Vocabulary (in):  run_event {type: message_sending|message_received|message_read|step|
//                   specialty_loaded|specialty_updated, text?, run_id?, client_msg_id?, conv?, offline?}
// Classic stub:     the classic stream has no receipt of its own: the first thing Blaze sends back
//                   (status, tool, delta, route, done) is Sent and Read at once; a network error
//                   keeps it at Sending; any other error is Sent (Blaze had it).
// Out: { kind: "status", state: sending|sent|read, offline? } and { kind: "step", text } and
//      { kind: "end" }.
// Runs in the browser (window.RunEvents) and under node (module.exports) for ci/runevents.test.js.
(function (root) {
  "use strict";
  const P = () => (root && root.PRIVACY) || (typeof require === "function" ? require("./privacy.js") : null);
  const ST = () => (root && root.STEPS) || (typeof require === "function" ? require("./steps.js") : null);
  const MAX = 60;
  const PILL_DELAY_MS = 2000;
  // Last-resort lines only: a specialty pill says what the help is FOR from the event's own detail
  // (helpLine), or the request; these show only when neither says anything usable (user, 2026-10-23).
  const SPECIALTY_LOADED = "Bringing in a specialist", SPECIALTY_UPDATED = "Updating a specialist";
  const SPECIALIST_LOADED = "Bringing in a specialist", SPECIALIST_UPDATED = "Updating a specialist";
  const RANK = { sending: 0, sent: 1, read: 2 };

  // A step line safe for the pill: one line, no provider/model/owner names, no raw tool JSON,
  // arguments or tool ids, at most MAX characters. "" when nothing readable is left.
  function sanitize(s) {
    s = String(s == null ? "" : s).split("\n")[0].trim();
    s = s.replace(/[{\[][\s\S]*$/, "").replace(/\w+\([^)]*\)/g, "").replace(/\b[a-z]+(?:_[a-z0-9]+)+\b(?![_.]?\w)/g, "").replace(/[`"<>]/g, "");
    const p = P();
    if (p && (p.leaks(s) || p.leaksOwner(s))) return "";
    s = s.replace(/\s+/g, " ").replace(/^[\s:;,\-·|]+|[\s:;,\-·|]+$/g, "");
    if (!/[A-Za-z]{2}/.test(s)) return "";
    if ([...s].length > MAX) s = [...s].slice(0, MAX - 1).join("").trim() + "…";
    return s;
  }

  const isSpecialistLoad = n => /specialist/.test(n || "") && /^(dispatch|load|brief)/.test(n || "");
  const isSpecialistUpdate = n => /specialist/.test(n || "") && /^(apply|update)/.test(n || "");
  const offlineLine = m => /can't reach|couldn.t reach|offline|unreachable|no internet|network|connection/i.test(String((m && (m.message || m)) || ""));

  // ---- specialty / specialist lines: what the help is for, never which one ----
  const GENERIC_HELP = /\b(using|updating|updated|loading|loaded|bringing in|brought in|briefing|applying)\s+(an?|the)?\s*(specialty|specialist)s?\b/gi;
  const HELP_WORD = /\b(specialty|specialties|specialist|specialists|playbook|playbooks)\b/gi;
  const LEAD_ASK = /^(please|pls|hey blaze|blaze|ok|okay|so|from now on|now|can you|could you|would you|will you|i need you to|i want you to|help me|go ahead and)[\s,]+/i;
  const NON_VERB = /^(what|who|whom|whose|why|how|when|where|which|is|are|was|were|am|do|does|did|can|could|would|will|should|shall|may|might|i|i'm|im|my|we|our|you|your|it|its|the|a|an|this|that|these|those|there|here|hi|hello|hey|thanks|thank)\b/i;
  const reEsc = x => x.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  function cleanDetail(s, names) {
    s = String(s == null ? "" : s).split("\n")[0].trim();
    const e = s.search(/[.!?;:](\s|$)/); if (e > 0) s = s.slice(0, e);
    for (const n of names || []) if (n && String(n).trim().length >= 2) s = s.replace(new RegExp("\\b" + reEsc(String(n).trim()) + "\\b", "gi"), "");
    s = s.replace(/\([^)]*\)|\[[^\]]*\]/g, "").replace(GENERIC_HELP, "").replace(HELP_WORD, "")
      .replace(/\b[a-z0-9]+(?:-[a-z0-9]+)+\b(?![-.]?\w)/g, "")
      .replace(/\b(via|on|using|with|through)\s+[A-Z][\w.-]*(\s+[\w.-]+)?\s*$/i, "");
    s = s.replace(/\s+$/, "").replace(/(\s+(with|via|on|using|through|by|for|to|from|and))+$/i, "");
    s = sanitize(s).replace(/\.+$/, "").trim();
    for (let t = s.replace(LEAD_ASK, ""); t !== s; t = s.replace(LEAD_ASK, "")) s = t;
    s = s.replace(/^[\s,:;-]+/, "");
    if ([...s].length < 4 || (s.split(/\s+/).length < 2 && !s.includes("."))) return "";
    return s;
  }
  const lowerFirst = s => /^.[A-Z]/.test(s) ? s : s.charAt(0).toLowerCase() + s.slice(1);
  const gerund = s => { const w = s.split(/\s+/)[0] || ""; return w.length > 4 && /ing$/i.test(w); };
  function helpLine(updated, detail, request, names) {
    // already a help line (the Go adapter built it): keep it as it is
    if (/^(Getting help|Saving what I learned|Saving your rule)\b/.test(String(detail || ""))) return sanitize(detail);
    const d = cleanDetail(detail, names);
    if (d) {
      if (updated) return sanitize("Saving what I learned: " + lowerFirst(d));
      return sanitize((gerund(d) || NON_VERB.test(d) ? "Getting help with " : "Getting help to ") + lowerFirst(d));
    }
    const r = cleanDetail(request, names);
    if (r) {
      if (updated) return sanitize("Saving your rule: " + lowerFirst(r));
      return sanitize((gerund(r) || NON_VERB.test(r) ? "Getting help with: " : "Getting help to ") + lowerFirst(r));
    }
    return updated ? SPECIALTY_UPDATED : SPECIALTY_LOADED;
  }

  // Plain words for tools whose describer has only the generic "Using <tool name>": the pill never
  // shows a tool's name (contract: no raw tool names), so these name the real object instead.
  const parse = a => { if (a && typeof a === "object") return a; try { return JSON.parse(a || "{}") || {}; } catch (e) { return {}; } };
  const base = p => String(p || "").split(/[\\/]/).pop();
  const host = u => { try { return new URL(u).host.replace(/^www\./, ""); } catch (e) { return ""; } };
  const clipW = (x, n) => { x = String(x || "").split("\n")[0].trim(); return [...x].length > n ? [...x].slice(0, n - 1).join("").trim() + "…" : x; };
  function pillLine(name, a) {
    const v = (...ks) => { for (const k of ks) { const x = a[k]; if (typeof x === "string" && x.trim()) return x.split("\n")[0].trim(); } return ""; };
    const w = (p, o, alone) => o ? p + o : alone;
    switch (name) {
      case "csv_report": {
        const g = clipW(v("group_by"), 20), val = clipW(v("value"), 20);
        if (g && val) return "Charting " + val + " by " + g;
        if (g) return "Charting " + (base(v("path")) || "your CSV") + " by " + g;
        return w("Making a report from ", base(v("path")), "Making your CSV report");
      }
      case "csv_summary": return "Writing your summary";
      case "memory_save": case "memory_update": return w("Remembering ", clipW(v("subject"), 40), "Saving that to memory");
      case "memory_search": case "recall": return w("Checking what I remember about ", clipW(v("query"), 30), "Checking what I remember");
      case "schedule_add": case "reminder_add": case "set_reminder": {
        const t = clipW(v("text", "title", "what", "message", "task"), 36), at = clipW(v("at", "when", "time"), 16);
        return t ? "Setting a reminder to " + t.replace(/^to /i, "") : at ? "Setting a reminder for " + at : "Setting up a reminder";
      }
      case "get_weather": return w("Checking the weather in ", clipW(v("place", "location", "city"), 30), "Checking the weather");
      case "find_tools": return w("Finding tools for ", clipW(v("query"), 30), "Getting the right tools");
      case "search_chat": return w("Looking back through this chat for ", clipW(v("query"), 24), "Looking back through this chat");
      case "read_turn": case "read_artifact": return "Rereading an earlier result";
      case "open_view": return w("Opening ", clipW(v("view"), 20), "Opening a view");
      case "update_brief": return "Updating the project brief";
      case "music_play": return w("Playing ", clipW(v("query", "song", "title", "artist"), 36), "Playing music");
      case "media_download": return "Downloading your media";
      case "check_specialist": return "Checking on the work in progress";
      case "cancel_specialist": return "Stopping part of the work";
      case "message_specialist": return helpLine(false, v("message"), "", [v("name")]);
    }
    return "";
  }
  function fallbackLine(a) {
    const v = k => typeof a[k] === "string" ? a[k].split("\n")[0].trim() : "";
    if (v("path")) return "Working on " + base(v("path"));
    if (v("query")) return 'Looking up "' + clipW(v("query"), 36) + '"';
    if (v("url") && host(v("url"))) return "Working with " + host(v("url"));
    return "Working on it";
  }
  function toolLine(name, args) {
    const a = parse(args), own = pillLine(name, a);
    if (own) return own; // this app's own tools: their real object, before the generic describer
    const st = ST();
    const s = st ? st.describe(name, args) : "";
    const generic = !s || s === "Working on it" || s.toLowerCase() === ("using " + String(name || "").replace(/[_-]+/g, " ").trim()).toLowerCase();
    return generic ? fallbackLine(a) : s;
  }

  // The pill's step for one event ("" = no change). request: the user's message the turn answers
  // (what a specialty is for when its event says nothing).
  function stepOf(ev, d, request) {
    if (ev === "run_event" && d) {
      const names = [d.name, d._name];
      const detail = d.detail || d.task || d.text;
      if (d.type === "specialty_loaded") return helpLine(false, detail, request, names);
      if (d.type === "specialty_updated") return helpLine(true, detail, request, names);
      if (d.type === "step" && d.kind === "specialty") return helpLine(/updat/i.test(d.text || ""), detail, request, names);
      if (d.type === "step") return sanitize(d.text);
      return "";
    }
    if (ev === "tool" && d) {
      const a = parse(d.args);
      if (isSpecialistLoad(d.name)) return helpLine(false, a.brief || a.task, request, [a.name]);
      if (isSpecialistUpdate(d.name)) return a.action === "discard" ? "Setting aside the reviewed changes" : "Bringing in the reviewed changes";
      return sanitize(toolLine(d.name, d.args));
    }
    if (ev === "status") { const st = ST(); return sanitize(st ? st.plainStatus(d) : d); }
    return "";
  }

  // One message's delivery status. Transitions only move forward (sending → sent → read).
  function message(cmid) {
    const m = { cmid: cmid || "", state: "sending", offline: false, run: false };
    const to = s => { if (RANK[s] > RANK[m.state]) { m.state = s; m.offline = false; return true; } return false; };
    m.apply = (ev, d) => {
      if (ev === "run_event" && d) {
        if (d.client_msg_id && m.cmid && d.client_msg_id !== m.cmid) return false;
        if (d.client_msg_id && !m.cmid) m.cmid = d.client_msg_id;
        switch (d.type) {
          case "message_sending":
            m.run = true;
            if (m.state === "sending" && !!d.offline !== m.offline) { m.offline = !!d.offline; return true; }
            return false;
          case "message_received": m.run = true; return to("sent");
          case "message_read": m.run = true; return to("sent") | to("read") ? true : false;
          default: return to("read"); // a step or specialty: Blaze is on it
        }
      }
      switch (ev) {
        // The classic stream has no receipt of its own: thinking is this app getting ready (it
        // happens offline too), so nothing moves until Blaze answers; then Sent and Read land together.
        case "thinking": return false;
        case "status": case "tool": case "delta": case "route": case "done": case "confirm": { const a = to("sent"), b = to("read"); return a || b; }
        case "error":
          if (m.state === "sending" && offlineLine(d)) { m.offline = true; return true; }
          return to("sent");
      }
      return false;
    };
    return m;
  }

  // The words under the user's message.
  const statusText = m => m.state === "read" ? "Read" : m.state === "sent" ? "Sent" : "Sending";

  // The pill's clock: no pill until the turn has been pending PILL_DELAY_MS.
  function pill(startedAt) {
    const p = { started: startedAt || 0, text: "", shown: false };
    p.set = t => { if (t && t !== p.text) { p.text = t; return true; } return false; };
    p.visible = now => !!p.text && (now - p.started) >= PILL_DELAY_MS;
    return p;
  }

  const api = { sanitize, stepOf, helpLine, cleanDetail, message, statusText, pill, PILL_DELAY_MS, MAX, SPECIALTY_LOADED, SPECIALTY_UPDATED, SPECIALIST_LOADED, SPECIALIST_UPDATED };
  if (typeof module === "object" && module.exports) module.exports = api;
  if (root) root.RunEvents = api;
})(typeof window !== "undefined" ? window : null);
