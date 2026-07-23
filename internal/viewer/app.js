let workloads = [];
let active = null;
let esAbort = null;
let stickToBottom = true;
let pendingText = "";
let flushTimer = null;
let about = {
  authorName: "Abdul Rehman",
  githubUrl: "https://github.com/Abdul-Rehman-DevOps",
  portfolioUrl: "https://abdulrehman.cz/",
  repoUrl: "https://github.com/Abdul-Rehman-DevOps/log-viewer",
  version: "0.5.3"
};
const $ = (id) => document.getElementById(id);
const IDLE_MS = 10 * 60 * 1000;
let idleTimer = null;
let lastPing = 0;
let sessionGone = false;

function goLoginTimeout() {
  endViewerSession("timeout");
}

function goLoginRestart() {
  endViewerSession("restart");
}

function endViewerSession(reason) {
  if (sessionGone) return;
  sessionGone = true;
  if (reason === "restart") sessionStorage.setItem("lv_restart", "1");
  else sessionStorage.setItem("lv_timeout", "1");
  stopLogs();
  fetch("/api/logout", { method: "POST" }).finally(() => {
    location.href = "/login?reason=" + encodeURIComponent(reason === "restart" ? "restart" : "timeout");
  });
}

function bumpActivity() {
  if (sessionGone) return;
  clearTimeout(idleTimer);
  idleTimer = setTimeout(goLoginTimeout, IDLE_MS);
  const now = Date.now();
  if (now - lastPing > 60_000) {
    lastPing = now;
    fetch("/api/me").then(async (res) => {
      if (res.status === 401) {
        goLoginTimeout();
        return;
      }
      try {
        const me = await res.json();
        if (me && me.error === "session_restarted") goLoginRestart();
        else if (me && me.error === "session_timeout") goLoginTimeout();
      } catch (_) {}
    }).catch(() => {});
  }
}

["mousemove", "keydown", "click", "scroll", "touchstart"].forEach((ev) => {
  document.addEventListener(ev, bumpActivity, { passive: true });
});
bumpActivity();

function normalizeTheme(theme) {
  if (theme === "light" || theme === "nord-light" || theme === "solarized-light") return "light";
  return "dark";
}

function applyTheme(theme) {
  theme = normalizeTheme(theme);
  document.documentElement.setAttribute("data-theme", theme);
  localStorage.setItem("lv_theme", theme);
  const meta = document.querySelector('meta[name="theme-color"]');
  if (meta) meta.setAttribute("content", theme === "dark" ? "#121212" : "#F5F5F5");
  const btn = $("themeBtn");
  if (btn) {
    // Dark → show sun (switch to light). Light → show moon (switch to dark).
    btn.textContent = theme === "dark" ? "☀" : "☾";
    btn.title = theme === "dark" ? "Switch to light" : "Switch to dark";
  }
}
applyTheme(localStorage.getItem("lv_theme") || "dark");

function fillAbout() {
  const set = (id, href) => { const el = $(id); if (el && href) el.href = href; };
  set("linkPortfolio", about.portfolioUrl || "https://abdulrehman.cz/");
  set("linkGithub", about.githubUrl || "https://github.com/Abdul-Rehman-DevOps");
  set("linkRepo", about.repoUrl || "https://github.com/Abdul-Rehman-DevOps/log-viewer");
}

function openAbout() {
  fillAbout();
  $("aboutModal").classList.remove("hidden");
}
function closeAbout() {
  $("aboutModal").classList.add("hidden");
}

async function api(path) {
  const res = await fetch(path);
  if (res.status === 401) {
    let reason = "auth";
    try {
      const body = await res.clone().json();
      if (body && body.error === "session_restarted") reason = "restart";
      else if (body && body.error === "session_timeout") reason = "timeout";
    } catch (_) {}
    if (reason === "restart") goLoginRestart();
    else if (reason === "timeout") goLoginTimeout();
    else location.href = "/login";
    throw new Error("auth");
  }
  if (res.status === 403) { location.href = "/setup"; throw new Error("setup"); }
  if (!res.ok) throw new Error(await res.text());
  bumpActivity();
  return res.json();
}

function renderList() {
  const q = $("q").value.toLowerCase();
  const items = workloads.filter((w) =>
    (w.namespace + "/" + w.kind + "/" + w.name).toLowerCase().includes(q)
  );
  if (!items.length) {
    $("list").innerHTML = "<div class=\"empty\">No workloads match admin settings</div>";
    return;
  }
  const byNs = {};
  items.forEach((w) => {
    if (!byNs[w.namespace]) byNs[w.namespace] = [];
    byNs[w.namespace].push(w);
  });
  let html = "";
  Object.keys(byNs).sort().forEach((ns) => {
    html += "<div class=\"ns-section\">namespace · " + ns + "</div>";
    byNs[ns].forEach((w) => {
      const idx = workloads.indexOf(w);
      const isActive = active && active.namespace === w.namespace && active.name === w.name && active.kind === w.kind;
      html += "<div class=\"item " + (isActive ? "active" : "") + "\" data-i=\"" + idx + "\" role=\"button\" tabindex=\"0\">" +
        "<div class=\"name\"><span class=\"kind\">" + w.kind + "</span>" + w.name + "</div>" +
        "<div class=\"meta\">" + w.ready + "/" + w.replicas + " ready · " + w.pods.length + " pods</div></div>";
    });
  });
  $("list").innerHTML = html;
  document.querySelectorAll(".item").forEach((el) => {
    const go = () => select(workloads[+el.dataset.i]);
    el.onclick = go;
    el.onkeydown = (e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); go(); } };
  });
}

function scrollLive() {
  const el = $("log");
  requestAnimationFrame(() => {
    el.scrollTop = el.scrollHeight;
    requestAnimationFrame(() => { el.scrollTop = el.scrollHeight; });
  });
  $("eof").classList.remove("hidden");
}

async function select(w) {
  active = w;
  renderList();
  $("sel").textContent = w.kind + " / " + w.namespace + " / " + w.name;
  $("pod").hidden = false;
  $("container").hidden = false;
  $("pod").innerHTML = w.pods.length
    ? ["<option value=\"__all__\" selected>All pods (" + w.pods.length + ")</option>"]
        .concat(w.pods.map((p) => "<option value=\"" + p + "\">" + p + "</option>")).join("")
    : "<option value=\"\">no pods</option>";
  await loadContainers();
  follow(); // always live on select — no Start button
}

async function loadContainers() {
  if (!active || !active.pods.length) {
    $("container").innerHTML = "<option value=\"\">default</option>";
    return;
  }
  const sample = $("pod").value === "__all__" ? active.pods[0] : $("pod").value;
  if (!sample) return;
  try {
    const data = await api("/api/pods/containers?namespace=" + encodeURIComponent(active.namespace) + "&pod=" + encodeURIComponent(sample));
    const list = data.containers || [];
    $("container").innerHTML = list.length
      ? list.map((c) => "<option>" + c + "</option>").join("")
      : "<option value=\"\">default</option>";
  } catch (e) {
    $("container").innerHTML = "<option value=\"\">default</option>";
  }
}

function stopLogs() {
  if (esAbort) { esAbort.abort(); esAbort = null; }
  if (flushTimer) { clearTimeout(flushTimer); flushTimer = null; }
  pendingText = "";
}

function selectedPods() {
  if (!active) return [];
  const v = $("pod").value;
  if (v === "__all__") return active.pods.slice();
  return v ? [v] : [];
}

function escapeHtml(s) {
  return String(s)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

function levelClass(level) {
  const u = String(level).toUpperCase();
  if (/^(ERROR|ERR|FATAL|CRITICAL|PANIC)$/.test(u)) return "err";
  if (/^(WARN|WARNING)$/.test(u)) return "warn";
  if (/^(DEBUG|TRACE)$/.test(u)) return "debug";
  return "info";
}

const ANSI_FG = {
  30: "var(--ansi-black)", 31: "var(--ansi-red)", 32: "var(--ansi-green)", 33: "var(--ansi-yellow)",
  34: "var(--ansi-blue)", 35: "var(--ansi-magenta)", 36: "var(--ansi-cyan)", 37: "var(--ansi-white)",
  90: "var(--ansi-bright-black)", 91: "var(--ansi-bright-red)", 92: "var(--ansi-bright-green)",
  93: "var(--ansi-bright-yellow)", 94: "var(--ansi-bright-blue)", 95: "var(--ansi-bright-magenta)",
  96: "var(--ansi-bright-cyan)", 97: "var(--ansi-bright-white)"
};

function hasAnsi(s) {
  return /\x1b\[[0-9;]*m/.test(s);
}

function ansi256(n) {
  if (n < 16) {
    const map = [30, 31, 32, 33, 34, 35, 36, 37, 90, 91, 92, 93, 94, 95, 96, 97];
    return ANSI_FG[map[n]] || "var(--ansi-white)";
  }
  if (n >= 232) {
    const v = 8 + (n - 232) * 10;
    return "rgb(" + v + "," + v + "," + v + ")";
  }
  const c = n - 16;
  const r = Math.floor(c / 36), g = Math.floor((c % 36) / 6), b = c % 6;
  const to = (x) => (x === 0 ? 0 : 55 + x * 40);
  return "rgb(" + to(r) + "," + to(g) + "," + to(b) + ")";
}

/** Append ANSI-colored text as real DOM nodes (never HTML strings). */
function appendAnsi(parent, input) {
  let cur = null;
  let i = 0;
  const ensure = (styles) => {
    cur = document.createElement("span");
    if (styles && styles.length) cur.style.cssText = styles.join(";");
    parent.appendChild(cur);
  };
  ensure([]);
  while (i < input.length) {
    if (input.charCodeAt(i) === 0x1b && input[i + 1] === "[") {
      let j = i + 2;
      while (j < input.length && !/[a-zA-Z]/.test(input[j])) j++;
      const cmd = input[j];
      const params = input.slice(i + 2, j);
      i = j + 1;
      if (cmd !== "m") continue;
      if (!params || params === "0") { ensure([]); continue; }
      const parts = params.split(";").map((x) => parseInt(x, 10)).filter((n) => !isNaN(n));
      const styles = [];
      for (let p = 0; p < parts.length; p++) {
        const n = parts[p];
        if (n === 1) styles.push("font-weight:600");
        else if (n === 2) styles.push("opacity:.75");
        else if (n === 3) styles.push("font-style:italic");
        else if (n === 4) styles.push("text-decoration:underline");
        else if (ANSI_FG[n]) styles.push("color:" + ANSI_FG[n]);
        else if (n === 38 && parts[p + 1] === 5 && parts[p + 2] != null) {
          styles.push("color:" + ansi256(parts[p + 2]));
          p += 2;
        } else if (n === 38 && parts[p + 1] === 2 && parts[p + 4] != null) {
          styles.push("color:rgb(" + parts[p + 2] + "," + parts[p + 3] + "," + parts[p + 4] + ")");
          p += 4;
        }
      }
      ensure(styles);
      continue;
    }
    // gather run of plain text
    let k = i;
    while (k < input.length && !(input.charCodeAt(k) === 0x1b && input[k + 1] === "[")) k++;
    cur.appendChild(document.createTextNode(input.slice(i, k)));
    i = k;
  }
}

const BADGE_COLORS = [
  "#2dd4bf", "#a78bfa", "#a3e635", "#60a5fa", "#fbbf24",
  "#f472b6", "#22d3ee", "#c084fc", "#4ade80", "#fb923c"
];

function badgeColor(pod) {
  let h = 2166136261;
  for (let i = 0; i < pod.length; i++) {
    h ^= pod.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }
  return BADGE_COLORS[(h >>> 0) % BADGE_COLORS.length];
}

function badgeLabel(pod) {
  const parts = pod.split("-");
  let base = pod;
  if (parts.length >= 3) base = parts.slice(0, -2).join("-");
  if (base.length > 18) base = base.slice(0, 16) + "…";
  const id = pod.length ? pod.charAt(pod.length - 1) : "?";
  return "_" + id + "/" + base;
}

function formatTs(ts) {
  const m = String(ts).match(/(\d{4})-(\d{2})-(\d{2})[ T](\d{2}:\d{2}:\d{2})/);
  if (m) return m[2] + "/" + m[3] + "/" + m[1] + " " + m[4];
  return ts;
}

function node(tag, className, text) {
  const n = document.createElement(tag);
  if (className) n.className = className;
  if (text != null && text !== "") n.textContent = text;
  return n;
}

function detectLevel(text) {
  const plain = String(text).replace(/\x1b\[[0-9;]*m/g, "");
  const m = plain.match(/"level"\s*:\s*"([^"]+)"/i)
    || plain.match(/\blevel[=:]["']?([A-Za-z]+)/i)
    || plain.match(/\b(ERROR|ERR|FATAL|CRITICAL|PANIC|WARN|WARNING|INFO|LOG|DEBUG|TRACE)\b/i);
  return m ? levelClass(m[1]) : "info";
}

/** Parse Python / k9s-style: [name] HH:MM:SS LEVEL logger message */
function parseAppLog(msg) {
  const plain = String(msg).replace(/\x1b\[[0-9;]*m/g, "");
  const LEVEL = "DEBUG|INFO|WARNING|WARN|ERROR|CRITICAL|FATAL|LOG|TRACE|ERR|PANIC";

  // name  HH:MM:SS  LEVEL  logger  message   (k9s / python logging)
  let m = plain.match(new RegExp(
    "^(\\S+)\\s+(\\d{2}:\\d{2}:\\d{2}(?:[.,]\\d+)?)\\s+(" + LEVEL + ")\\s+(\\S+)\\s+(.*)$", "i"
  ));
  if (m) return { name: m[1], time: m[2], level: m[3], logger: m[4], message: m[5] };

  // HH:MM:SS  LEVEL  logger  message
  m = plain.match(new RegExp(
    "^(\\d{2}:\\d{2}:\\d{2}(?:[.,]\\d+)?)\\s+(" + LEVEL + ")\\s+(\\S+)\\s+(.*)$", "i"
  ));
  if (m) return { time: m[1], level: m[2], logger: m[3], message: m[4] };

  // LEVEL: rest  (uvicorn / fastapi access)
  m = plain.match(new RegExp("^(" + LEVEL + ")\\s*:\\s*(.*)$", "i"));
  if (m) return { level: m[1], message: m[2] };

  // name LEVEL: rest
  m = plain.match(new RegExp("^(\\S+)\\s+(" + LEVEL + ")\\s*:\\s*(.*)$", "i"));
  if (m) return { name: m[1], level: m[2], message: m[3] };

  // Leading LEVEL then rest
  m = plain.match(new RegExp("^(" + LEVEL + ")\\s+(.*)$", "i"));
  if (m) return { level: m[1], message: m[2] };

  return null;
}

function tryParseJsonLine(msg) {
  const t = String(msg).trim();
  if (!t.startsWith("{") && !t.startsWith("[")) return null;
  try { return JSON.parse(t); } catch (_) { return null; }
}

function appendJsonValue(parent, val) {
  if (val === null) {
    parent.appendChild(node("span", "json-null", "null"));
    return;
  }
  if (typeof val === "boolean") {
    parent.appendChild(node("span", "json-bool", String(val)));
    return;
  }
  if (typeof val === "number") {
    parent.appendChild(node("span", "json-num", String(val)));
    return;
  }
  if (typeof val === "string") {
    parent.appendChild(document.createTextNode("\""));
    if (/^(ERROR|ERR|FATAL|CRITICAL|PANIC|WARN|WARNING|INFO|LOG|DEBUG|TRACE)$/i.test(val)) {
      parent.appendChild(node("span", "lv-" + levelClass(val), val));
    } else if (/^https?:\/\//i.test(val)) {
      parent.appendChild(node("span", "lv-url", val));
    } else {
      parent.appendChild(node("span", "json-str", val));
    }
    parent.appendChild(document.createTextNode("\""));
    return;
  }
  if (Array.isArray(val)) {
    parent.appendChild(document.createTextNode("["));
    val.forEach((v, i) => {
      if (i) parent.appendChild(document.createTextNode(", "));
      appendJsonValue(parent, v);
    });
    parent.appendChild(document.createTextNode("]"));
    return;
  }
  if (typeof val === "object") {
    appendJsonObject(parent, val);
  }
}

function appendJsonObject(parent, obj) {
  parent.appendChild(document.createTextNode("{"));
  const keys = Object.keys(obj);
  keys.forEach((k, i) => {
    if (i) parent.appendChild(document.createTextNode(", "));
    parent.appendChild(node("span", "json-key", "\"" + k + "\""));
    parent.appendChild(document.createTextNode(": "));
    appendJsonValue(parent, obj[k]);
  });
  parent.appendChild(document.createTextNode("}"));
}

/** Highlight URLs + HTTP status in a plain message via DOM. */
function appendMessageText(parent, text) {
  const re = /(https?:\/\/[^\s"'<>]+)|\b(20\d|23[0-4]|[45]\d{2})\b|\b(OK|Not Found|Created|No Content)\b/g;
  let last = 0;
  let m;
  while ((m = re.exec(text)) !== null) {
    if (m.index > last) parent.appendChild(document.createTextNode(text.slice(last, m.index)));
    if (m[1]) parent.appendChild(node("span", "lv-url", m[1]));
    else if (m[2]) {
      const code = m[2];
      const cls = /^[45]/.test(code) ? "lv-bad" : "lv-ok";
      parent.appendChild(node("span", cls, code));
    } else if (m[3]) {
      const cls = /Not Found/i.test(m[3]) ? "lv-bad" : "lv-ok";
      parent.appendChild(node("span", cls, m[3]));
    }
    last = m.index + m[0].length;
  }
  if (last < text.length) parent.appendChild(document.createTextNode(text.slice(last)));
}

function appendLogfmt(parent, msg) {
  const re = /([a-zA-Z_][\w.-]*)=("[^"]*"|'[^']*'|[^\s]+)/g;
  const pairs = msg.match(re);
  if (!pairs || (pairs.length < 2 && !/^[a-zA-Z_][\w.-]*=/.test(msg.trim()))) return false;
  let last = 0;
  let m;
  const walker = new RegExp(re.source, "g");
  while ((m = walker.exec(msg)) !== null) {
    if (m.index > last) parent.appendChild(document.createTextNode(msg.slice(last, m.index)));
    parent.appendChild(node("span", "lv-k", m[1]));
    parent.appendChild(document.createTextNode("="));
    const val = m[2];
    const bare = val.replace(/^["']|["']$/g, "");
    if (/^(ERROR|ERR|FATAL|CRITICAL|PANIC|WARN|WARNING|INFO|LOG|DEBUG|TRACE)$/i.test(bare)) {
      const q = val.startsWith("\"") || val.startsWith("'") ? val[0] : "";
      if (q) parent.appendChild(document.createTextNode(q));
      parent.appendChild(node("span", "lv-" + levelClass(bare), bare));
      if (q) parent.appendChild(document.createTextNode(q));
    } else {
      parent.appendChild(document.createTextNode(val));
    }
    last = m.index + m[0].length;
  }
  if (last < msg.length) parent.appendChild(document.createTextNode(msg.slice(last)));
  return true;
}

function fillMsg(parent, msg) {
  if (hasAnsi(msg)) {
    appendAnsi(parent, msg);
    return;
  }
  const obj = tryParseJsonLine(msg);
  if (obj !== null) {
    if (Array.isArray(obj)) {
      parent.appendChild(document.createTextNode("["));
      obj.forEach((v, i) => {
        if (i) parent.appendChild(document.createTextNode(", "));
        appendJsonValue(parent, v);
      });
      parent.appendChild(document.createTextNode("]"));
    } else if (typeof obj === "object") {
      appendJsonObject(parent, obj);
    } else {
      appendJsonValue(parent, obj);
    }
    return;
  }
  if (appendLogfmt(parent, msg)) return;

  const parsed = parseAppLog(msg);
  if (parsed) {
    if (parsed.name) {
      parent.appendChild(node("span", "log-logger", parsed.name));
      parent.appendChild(document.createTextNode("  "));
    }
    if (parsed.time) {
      parent.appendChild(node("span", "log-app-ts", parsed.time));
      parent.appendChild(document.createTextNode("  "));
    }
    if (parsed.level) {
      parent.appendChild(node("span", "lv-" + levelClass(parsed.level), parsed.level.toUpperCase()));
      parent.appendChild(document.createTextNode("  "));
    }
    if (parsed.logger) {
      parent.appendChild(node("span", "log-logger", parsed.logger));
      parent.appendChild(document.createTextNode("  "));
    }
    appendMessageText(parent, parsed.message || "");
    return;
  }
  appendMessageText(parent, msg);
}

function buildLogRow(raw) {
  if (!raw) return null;
  let pod = "";
  let ts = "";
  let msg = raw;

  const head = raw.match(/^\[([^\]]+)\]\s*(.*)$/);
  if (head) {
    pod = head[1];
    let rest = head[2];
    const tm = rest.match(/^(\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}:\d{2}(?:[.,]\d+)?(?:\s*[A-Z]{2,5})?)\s+(.*)$/);
    if (tm) {
      ts = formatTs(tm[1]);
      rest = tm[2];
    }
    msg = rest;
  }

  const level = detectLevel(msg);
  const row = node("div", "log-row " + level);

  const badge = node("span", "log-badge", pod ? badgeLabel(pod) : "log");
  badge.style.background = pod ? badgeColor(pod) : "#52525b";
  if (pod) badge.title = pod;
  row.appendChild(badge);

  row.appendChild(node("span", "log-ts", ts || ""));
  row.appendChild(node("span", "log-dot " + level)).title = level.toUpperCase();

  const body = node("span", "log-msg");
  fillMsg(body, msg);
  row.appendChild(body);
  return row;
}

function flushPending() {
  flushTimer = null;
  if (!pendingText) return;
  const el = $("log");
  if (el.querySelector(".empty")) el.innerHTML = "";
  const parts = pendingText.split("\n");
  pendingText = parts.pop() || "";
  const frag = document.createDocumentFragment();
  parts.forEach((line) => {
    if (!line) return;
    const row = buildLogRow(line);
    if (row) frag.appendChild(row);
  });
  if (frag.childNodes.length) el.appendChild(frag);
  if (stickToBottom) scrollLive();
  updateScrollEndBtn();
}

function queueChunk(text) {
  pendingText += text;
  if (!flushTimer) flushTimer = setTimeout(flushPending, 24);
}

function updateScrollEndBtn() {
  const btn = $("scrollEnd");
  if (!btn) return;
  btn.classList.toggle("show", !stickToBottom);
}

async function follow(opts) {
  const clear = !opts || opts.clear !== false;
  stopLogs();
  const pods = selectedPods();
  if (!active || !pods.length) {
    $("log").innerHTML = "<div class=\"empty\">No pods available for this workload</div>";
    $("eof").classList.add("hidden");
    return;
  }
  const ctrl = new AbortController();
  esAbort = ctrl;
  if (clear) {
    $("log").innerHTML = "";
    stickToBottom = true;
  }
  $("eof").classList.remove("hidden");

  const url = "/api/logs?namespace=" + encodeURIComponent(active.namespace) +
    "&pods=" + encodeURIComponent(pods.join(",")) +
    "&container=" + encodeURIComponent($("container").value || "") +
    "&tail=" + (clear ? "150" : "0");

  try {
    const res = await fetch(url, { signal: ctrl.signal });
    if (!res.ok) {
      queueChunk("\n[error] " + (await res.text()) + "\n");
      flushPending();
      $("eof").classList.add("hidden");
      return;
    }
    const reader = res.body.getReader();
    const dec = new TextDecoder();
    while (true) {
      const { value, done } = await reader.read();
      if (done) break;
      queueChunk(dec.decode(value, { stream: true }));
    }
    flushPending();
    if (stickToBottom) scrollLive();
  } catch (e) {
    if (e.name === "AbortError") return;
    queueChunk("\n[stream error] " + (e.message || e) + " — reconnecting…\n");
    flushPending();
  } finally {
    if (esAbort === ctrl) {
      esAbort = null;
      // Keep live: reconnect without wiping history.
      if (active) setTimeout(() => { if (active && !esAbort) follow({ clear: false }); }, 1500);
    }
  }
}

$("learnMore").onclick = openAbout;
$("aboutClose").onclick = closeAbout;
$("aboutModal").onclick = (e) => { if (e.target === $("aboutModal")) closeAbout(); };
document.addEventListener("keydown", (e) => { if (e.key === "Escape") closeAbout(); });

$("q").oninput = renderList;
$("pod").onchange = async () => { await loadContainers(); follow(); };
$("container").onchange = () => follow();
$("themeBtn").onclick = () => {
  const cur = document.documentElement.getAttribute("data-theme");
  applyTheme(cur === "dark" ? "light" : "dark");
};
$("logout").onclick = async () => {
  await fetch("/api/logout", { method: "POST" });
  location.href = "/login";
};
$("log").addEventListener("scroll", () => {
  const el = $("log");
  const nearBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
  stickToBottom = nearBottom;
  if (nearBottom) $("eof").classList.remove("hidden");
  else $("eof").classList.add("hidden");
  updateScrollEndBtn();
});
if ($("scrollEnd")) {
  $("scrollEnd").onclick = () => {
    stickToBottom = true;
    scrollLive();
    updateScrollEndBtn();
  };
}

(async () => {
  const me = await api("/api/me");
  if (me.needsSetup) { location.href = "/setup"; return; }
  if (!me.authenticated) { location.href = "/login"; return; }
  $("title").textContent = me.title || "Log Viewer";
  about = {
    authorName: me.authorName || about.authorName,
    githubUrl: me.githubUrl || about.githubUrl,
    portfolioUrl: me.portfolioUrl || about.portfolioUrl,
    repoUrl: me.repoUrl || about.repoUrl,
    version: me.version || about.version
  };
  fillAbout();
  const data = await api("/api/workloads");
  workloads = data.workloads || [];
  if (data.config) {
    about.authorName = data.config.authorName || about.authorName;
    about.githubUrl = data.config.githubUrl || about.githubUrl;
    about.portfolioUrl = data.config.portfolioUrl || about.portfolioUrl;
    about.repoUrl = data.config.repoUrl || about.repoUrl;
    fillAbout();
  }
  renderList();
  setInterval(async () => {
    try {
      const d = await api("/api/workloads");
      workloads = d.workloads || [];
      renderList();
    } catch (e) {}
  }, 5000);
})();
