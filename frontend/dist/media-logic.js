// v0.19 Blaze Music / Video: the pure rules the player uses (the same as Blaze Mobile's
// MediaLogic.kt / Extras.kt / VideoSmart.kt and internal/media). Node-testable: ci/media.test.js.
(function (root) {
  const clamp = (v, lo, hi) => Math.max(lo, Math.min(hi, v));

  // A shuffle that starts with `start` (the song tapped plays first); deterministic for a seed.
  function shuffleOrder(size, start, seed) {
    if (size <= 0) return [];
    const s = clamp(start, 0, size - 1);
    const rest = [];
    for (let i = 0; i < size; i++) if (i !== s) rest.push(i);
    let x = (seed >>> 0) || 1;
    const rnd = () => { x ^= x << 13; x ^= x >>> 17; x ^= x << 5; return ((x >>> 0) % 1e9) / 1e9; };
    for (let i = rest.length - 1; i > 0; i--) { const j = Math.floor(rnd() * (i + 1)); [rest[i], rest[j]] = [rest[j], rest[i]]; }
    return [s, ...rest];
  }
  const ord = (size, o) => (o && o.length === size ? o : Array.from({ length: size }, (_, i) => i));

  // What plays after `cur` (-1 = the queue ends). repeat: off | all | one.
  function nextIndex(cur, size, repeat, order) {
    if (size <= 0 || cur < 0 || cur >= size) return -1;
    if (repeat === "one") return cur;
    const o = ord(size, order), p = o.indexOf(cur);
    if (p < 0) return -1;
    if (p + 1 < o.length) return o[p + 1];
    return repeat === "all" ? o[0] : -1;
  }
  function prevIndex(cur, size, repeat, order) {
    if (size <= 0 || cur < 0 || cur >= size) return -1;
    const o = ord(size, order), p = o.indexOf(cur);
    if (p < 0) return -1;
    if (p > 0) return o[p - 1];
    return repeat === "all" ? o[o.length - 1] : -1;
  }
  function upNext(cur, size, repeat, order, count = 50) {
    const o = ord(size, order), p = o.indexOf(cur);
    if (p < 0) return o.slice(0, count);
    return [...o.slice(p + 1), ...(repeat === "all" ? o.slice(0, p) : [])].slice(0, count);
  }
  const nextRepeat = r => (r === "off" ? "all" : r === "all" ? "one" : "off");

  // Crossfade-style volume (in over the first fadeMs, out over the last) and the sleep fade.
  function fadeVolume(pos, dur, fade) {
    if (fade <= 0 || dur <= 0 || dur < fade * 3) return 1;
    return Math.max(Math.min(clamp(pos / fade, 0, 1), clamp((dur - pos) / fade, 0, 1)), 0.05);
  }
  const sleepVolume = (left, fade = 30000) => (left >= fade ? 1 : clamp(left / fade, 0, 1));

  // The synced lyric line at pos (-1 before the first).
  function lrcCurrent(lines, pos) {
    let lo = 0, hi = lines.length - 1, ans = -1;
    while (lo <= hi) { const mid = (lo + hi) >> 1; if (lines[mid].ms <= pos) { ans = mid; lo = mid + 1; } else hi = mid - 1; }
    return ans;
  }

  // Video: resume unless barely started or practically finished; Skip intro window; Up next countdown.
  const resumeAt = (saved, dur) => (saved < 5000 || (dur > 0 && saved > dur - 10000) ? 0 : saved);
  const showSkip = (pos, introEnd) => introEnd > 0 && pos >= 2000 && pos < introEnd - 3000;
  const countdown = (startedAt, now, seconds = 5) => { const left = seconds - Math.floor((now - startedAt) / 1000); return left <= 0 ? -1 : left; };
  const SPEEDS = [0.5, 0.75, 1, 1.25, 1.5, 1.75, 2];
  const nextSpeed = s => SPEEDS.find(x => x > s + 0.001) || SPEEDS[0];
  const speedLabel = s => String(s).replace(/^0\./, "0.") + "\u00d7";

  // "1:02:05" / "4:13".
  function clock(ms) {
    const t = Math.max(0, Math.floor((ms || 0) / 1000)), h = Math.floor(t / 3600), m = Math.floor((t % 3600) / 60), s = t % 60;
    return h > 0 ? `${h}:${String(m).padStart(2, "0")}:${String(s).padStart(2, "0")}` : `${m}:${String(s).padStart(2, "0")}`;
  }
  // Episode marker for series grouping in the UI ("Show S01E02", "Show - Episode 3").
  function episode(title) {
    let t = String(title || "").replace(/_/g, " ").trim();
    if (!t.includes(" ") && (t.match(/\./g) || []).length >= 2) t = t.replace(/\./g, " ");
    const pats = [/^(.*?)[\s._\-:|]*\bS(\d{1,2})\s*[._\-]?\s*E(\d{1,3})\b/i, /^(.*?)[\s._\-:|]*\b(\d{1,2})x(\d{1,3})\b/i];
    for (const p of pats) { const m = t.match(p); if (m) { const s = m[1].replace(/[\s._\-:|(\[]+$/, "").trim(); return s ? { series: s, season: +m[2], episode: +m[3] } : null; } }
    const m = t.match(/^(.*?)[\s._\-:|(\[]*\b(?:Episode|Ep\.?|Part|Pt\.?|Chapter|#)\s*(\d{1,3})\b/i);
    if (m) { const s = m[1].replace(/[\s._\-:|(\[]+$/, "").trim(); return s ? { series: s, season: 1, episode: +m[2] } : null; }
    return null;
  }
  // Library search: every word in title / artist / album.
  function search(tracks, q) {
    const words = String(q || "").toLowerCase().split(/\s+/).filter(Boolean);
    if (!words.length) return tracks;
    return tracks.filter(t => { const hay = `${t.t} ${t.a || ""} ${t.al || ""}`.toLowerCase(); return words.every(w => hay.includes(w)); });
  }
  function groupBy(tracks, key) {
    const m = new Map();
    tracks.forEach(t => { const k = key(t); if (!m.has(k)) m.set(k, []); m.get(k).push(t); });
    return [...m.entries()].sort((a, b) => a[0].toLowerCase().localeCompare(b[0].toLowerCase()));
  }
  const artists = tracks => groupBy(tracks, t => t.a || "Unknown artist");
  const albums = tracks => groupBy(tracks, t => t.al || t.a || "Singles");

  const api = { shuffleOrder, nextIndex, prevIndex, upNext, nextRepeat, fadeVolume, sleepVolume, lrcCurrent, resumeAt, showSkip, countdown, SPEEDS, nextSpeed, speedLabel, clock, episode, search, artists, albums };
  if (typeof module !== "undefined" && module.exports) module.exports = api;
  else root.BlazeMediaLogic = api;
})(typeof window !== "undefined" ? window : this);
