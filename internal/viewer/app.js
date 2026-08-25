let workloads = [];
let archivedWorkloads = [];
let displayWorkloads = [];
let unhealthy = [];
let active = null;
let activeProblem = null;
let usePreviousLogs = false;
let logMode = "live"; // live | history
let archiveInfo = { enabled: false, retentionDays: 3 };
let sideTab = "workloads";
let esAbort = null;
let stickToBottom = true;
let pendingText = "";
let flushTimer = null;
let about = {
  authorName: "Abdul Rehman",
  githubUrl: "https://github.com/Abdul-Rehman-DevOps",
  portfolioUrl: "https://abdulrehman.cz/",
  repoUrl: "https://github.com/Abdul-Rehman-DevOps/log-viewer",
  version: "0.6.0"
};
const $ = (id) => document.getElementById(id);
// Keep in sync with auth.SessionTTL (8h sliding idle).
const IDLE_MS = 8 * 60 * 60 * 1000;
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

function mergeDisplayWorkloads() {
  if (logMode !== "history" || !archiveInfo.enabled) {
    displayWorkloads = workloads.slice();
    return;
  }
  const map = new Map();
  workloads.forEach((w) => {
    const key = w.namespace + "/" + w.kind + "/" + w.name;
    map.set(key, Object.assign({}, w, { archivedOnly: false }));
  });
  archivedWorkloads.forEach((a) => {
    const key = a.namespace + "/" + a.kind + "/" + a.name;
    const live = map.get(key);
    if (live) {
      // Keep live pods; merge any archived pod names that vanished.
      const set = new Set(live.pods || []);
      (a.pods || []).forEach((p) => set.add(p));
      live.pods = Array.from(set).sort();
      live.archiveDays = a.days || [];
      live.fromArchive = true;
    } else {
      map.set(key, {
        kind: a.kind,
        namespace: a.namespace,
        name: a.name,
        replicas: 0,
        ready: 0,
        pods: a.pods || [],
        archivedOnly: true,
        fromArchive: true,
        archiveDays: a.days || [],
        lastSeen: a.lastSeen || ""
      });
    }
  });
  displayWorkloads = Array.from(map.values()).sort((a, b) => {
    if (a.namespace !== b.namespace) return a.namespace < b.namespace ? -1 : 1;
    if (a.kind !== b.kind) return a.kind < b.kind ? -1 : 1;
    return a.name < b.name ? -1 : 1;
  });
}

function renderList() {
  const q = $("q").value.toLowerCase();
  const source = logMode === "history" ? displayWorkloads : workloads;
  const items = source.filter((w) =>
    (w.namespace + "/" + w.kind + "/" + w.name).toLowerCase().includes(q)
  );
  if (!items.length) {
    $("list").innerHTML = logMode === "history"
      ? "<div class=\"empty\">No archived workloads in S3 yet (last " + retentionDays() + " days)</div>"
      : "<div class=\"empty\">No workloads match admin settings</div>";
    return;
  }
  const byNs = {};
  items.forEach((w) => {
    if (!byNs[w.namespace]) byNs[w.namespace] = [];
    byNs[w.namespace].push(w);
  });
  let html = "";
  Object.keys(byNs).sort().forEach((ns) => {
    html += "<div class=\"ns-section\"><span class=\"ns-label\">namespace</span> · <span class=\"ns-name\">" + ns + "</span></div>";
    byNs[ns].forEach((w) => {
      const idx = source.indexOf(w);
      const isActive = !activeProblem && active && active.namespace === w.namespace && active.name === w.name && active.kind === w.kind;
      let meta;
      if (w.archivedOnly) {
        const days = (w.archiveDays && w.archiveDays.length) ? w.archiveDays.length + " day(s)" : "S3";
        meta = "deleted · " + (w.pods || []).length + " archived pods · " + days;
      } else if (w.fromArchive && logMode === "history") {
        meta = w.ready + "/" + w.replicas + " ready · " + w.pods.length + " pods · S3 history";
      } else {
        meta = w.ready + "/" + w.replicas + " ready · " + w.pods.length + " pods";
      }
      html += "<div class=\"item " + (isActive ? "active" : "") + (w.archivedOnly ? " archived" : "") + "\" data-i=\"" + idx + "\" role=\"button\" tabindex=\"0\">" +
        "<div class=\"name\"><span class=\"kind\">" + w.kind + "</span>" + w.name +
        (w.archivedOnly ? " <span class=\"kind\" style=\"opacity:.7\">archived</span>" : "") + "</div>" +
        "<div class=\"meta\">" + meta + "</div></div>";
    });
  });
  $("list").innerHTML = html;
  document.querySelectorAll("#list .item").forEach((el) => {
    const go = () => select(source[+el.dataset.i]);
    el.onclick = go;
    el.onkeydown = (e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); go(); } };
  });
}

function updateAlertUI() {
  const n = unhealthy.length;
  const btn = $("alertBtn");
  const badge = $("alertBadge");
  const tab = $("tabProblems");
  const tabCount = $("problemsTabCount");
  if (badge) badge.textContent = String(n > 99 ? "99+" : n);
  if (tabCount) tabCount.textContent = String(n > 99 ? "99+" : n);
  if (btn) {
    const on = n > 0;
    btn.classList.toggle("active", on);
    btn.setAttribute("aria-pressed", on ? "true" : "false");
    btn.title = on
      ? (n + " failing pod" + (n === 1 ? "" : "s") + " — open Problems")
      : "No CrashLoop / Failed pods in allowed workloads";
  }
  if (tab) tab.classList.toggle("has-alert", n > 0);
}

function renderProblems() {
  const qEl = $("qProblems");
  const q = (qEl && qEl.value ? qEl.value : "").toLowerCase();
  const items = unhealthy.filter((p) =>
    (p.namespace + "/" + p.workload + "/" + p.pod + "/" + p.reason).toLowerCase().includes(q)
  );
  updateAlertUI();
  if (!items.length) {
    $("problemsList").innerHTML = unhealthy.length
      ? "<div class=\"empty\">No problems match this filter</div>"
      : "<div class=\"empty\">No CrashLoop or Failed pods in Admin-allowed workloads</div>";
    return;
  }
  const byNs = {};
  items.forEach((p) => {
    if (!byNs[p.namespace]) byNs[p.namespace] = [];
    byNs[p.namespace].push(p);
  });
  let html = "";
  Object.keys(byNs).sort().forEach((ns) => {
    html += "<div class=\"ns-section\"><span class=\"ns-label\">problems</span> · <span class=\"ns-name\">" + ns + "</span></div>";
    byNs[ns].forEach((p) => {
      const idx = unhealthy.indexOf(p);
      const isActive = activeProblem && activeProblem.pod === p.pod && activeProblem.namespace === p.namespace;
      const sev = p.severity || "error";
      html += "<div class=\"item problem sev-" + sev + " " + (isActive ? "active" : "") + "\" data-i=\"" + idx + "\" role=\"button\" tabindex=\"0\">" +
        "<div class=\"name\"><span class=\"kind\">" + escapeHtml(p.kind) + "</span>" + escapeHtml(p.workload) + "</div>" +
        "<div class=\"meta\">" + escapeHtml(p.pod) +
          (p.restartCount ? " · restarts " + p.restartCount : "") + "</div>" +
        "<span class=\"reason\">" + escapeHtml(p.reason || sev) + "</span></div>";
    });
  });
  $("problemsList").innerHTML = html;
  document.querySelectorAll("#problemsList .item").forEach((el) => {
    const go = () => selectProblem(unhealthy[+el.dataset.i]);
    el.onclick = go;
    el.onkeydown = (e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); go(); } };
  });
}

function setSideTab(tab) {
  sideTab = tab === "problems" ? "problems" : "workloads";
  $("tabWorkloads").classList.toggle("active", sideTab === "workloads");
  $("tabProblems").classList.toggle("active", sideTab === "problems");
  $("tabWorkloads").setAttribute("aria-selected", sideTab === "workloads" ? "true" : "false");
  $("tabProblems").setAttribute("aria-selected", sideTab === "problems" ? "true" : "false");
  $("workloadsPanel").classList.toggle("active", sideTab === "workloads");
  $("problemsPanel").classList.toggle("active", sideTab === "problems");
}

async function selectProblem(p) {
  if (!p) return;
  activeProblem = p;
  usePreviousLogs = !!p.previousLogs;
  active = {
    kind: p.kind,
    namespace: p.namespace,
    name: p.workload,
    pods: [p.pod],
    replicas: 1,
    ready: 0
  };
  setSideTab("problems");
  renderList();
  renderProblems();
  $("sel").textContent = p.reason + " · " + p.kind + " / " + p.namespace + " / " + p.workload;
  const note = $("prevNote");
  if (note) {
    note.classList.toggle("hidden", !usePreviousLogs);
    note.textContent = usePreviousLogs ? "previous container logs" : "";
  }
  $("pod").hidden = false;
  $("container").hidden = false;
  $("pod").innerHTML = "<option value=\"" + escapeHtml(p.pod) + "\" selected>" + escapeHtml(p.pod) + "</option>";
  if (p.container) {
    $("container").innerHTML = "<option value=\"" + escapeHtml(p.container) + "\" selected>" + escapeHtml(p.container) + "</option>";
  } else {
    await loadContainers();
  }
  follow();
}

async function select(w) {
  active = w;
  activeProblem = null;
  usePreviousLogs = false;
  const note = $("prevNote");
  if (note) {
    if (w.archivedOnly) {
      note.textContent = "archived (deleted from cluster)";
      note.classList.remove("hidden");
    } else {
      note.classList.add("hidden");
    }
  }
  mergeDisplayWorkloads();
  renderList();
  renderProblems();
  $("sel").textContent = w.kind + " / " + w.namespace + " / " + w.name + (w.archivedOnly ? " (archived)" : "");
  $("pod").hidden = false;
  $("container").hidden = false;
  const pods = w.pods || [];
  $("pod").innerHTML = pods.length
    ? ["<option value=\"__all__\" selected>All pods (" + pods.length + ")</option>"]
        .concat(pods.map((p) => "<option value=\"" + p + "\">" + p + "</option>")).join("")
    : "<option value=\"__all__\" selected>All archived pods</option>";
  if (w.archivedOnly) {
    $("container").innerHTML = "<option value=\"\">all containers</option>";
    followOrHistory();
    return;
  }
  await loadContainers();
  followOrHistory(); // live stream or wait for history Load
}

function pad2(n) { return String(n).padStart(2, "0"); }

function toLocalInputValue(d) {
  return d.getFullYear() + "-" + pad2(d.getMonth() + 1) + "-" + pad2(d.getDate()) +
    "T" + pad2(d.getHours()) + ":" + pad2(d.getMinutes());
}

function fromLocalInputValue(v) {
  if (!v) return null;
  const d = new Date(v);
  return Number.isNaN(d.getTime()) ? null : d;
}

function retentionDays() {
  return archiveInfo.retentionDays > 0 ? archiveInfo.retentionDays : 3;
}

function initHistoryRangeDefaults() {
  const to = new Date();
  const from = new Date(to.getTime() - 60 * 60 * 1000);
  const min = new Date(to.getTime() - retentionDays() * 24 * 60 * 60 * 1000);
  const fromEl = $("histFrom");
  const toEl = $("histTo");
  if (!fromEl || !toEl) return;
  fromEl.min = toLocalInputValue(min);
  toEl.min = toLocalInputValue(min);
  fromEl.max = toLocalInputValue(to);
  toEl.max = toLocalInputValue(to);
  if (!fromEl.value) fromEl.value = toLocalInputValue(from);
  if (!toEl.value) toEl.value = toLocalInputValue(to);
}

function applyArchiveUI() {
  const hint = $("histHint");
  const histBtn = $("modeHistory");
  const days = retentionDays();
  if (hint) {
    hint.textContent = archiveInfo.enabled
      ? ("S3 · last " + days + " days")
      : "S3 not configured";
  }
  if (histBtn) {
    histBtn.disabled = !archiveInfo.enabled;
    histBtn.title = archiveInfo.enabled
      ? ("Query archived logs (last " + days + " days)")
      : "Enable LOG_VIEWER_S3_* to use History";
  }
}

function setLogMode(mode) {
  if (mode === "history" && !archiveInfo.enabled) {
    mode = "live";
  }
  logMode = mode;
  const liveBtn = $("modeLive");
  const histBtn = $("modeHistory");
  const range = $("histRange");
  if (liveBtn) {
    liveBtn.classList.toggle("active", mode === "live");
    liveBtn.setAttribute("aria-pressed", mode === "live" ? "true" : "false");
  }
  if (histBtn) {
    histBtn.classList.toggle("active", mode === "history");
    histBtn.setAttribute("aria-pressed", mode === "history" ? "true" : "false");
  }
  if (range) range.classList.toggle("show", mode === "history");
  if (mode === "history") {
    initHistoryRangeDefaults();
    stopLogs();
    refreshArchivedWorkloads().then(() => {
      mergeDisplayWorkloads();
      renderList();
    });
    const eofLabel = $("eof") && $("eof").querySelector("span:nth-child(2)");
    if (eofLabel) eofLabel.textContent = "History · end of range";
    if (active) {
      $("log").innerHTML = "<div class=\"empty\">Pick From / To (date &amp; time) and click Load — works even if the Deployment/pods were deleted</div>";
      $("eof").classList.add("hidden");
    }
  } else {
    if (active && active.archivedOnly) {
      active = null;
      $("sel").textContent = "Select a Deployment or StatefulSet";
      $("pod").hidden = true;
      $("container").hidden = true;
      $("log").innerHTML = "<div class=\"empty\">Pick a live workload to stream logs</div>";
      $("eof").classList.add("hidden");
      const note = $("prevNote");
      if (note) note.classList.add("hidden");
    }
    mergeDisplayWorkloads();
    renderList();
    followOrHistory();
  }
}

function followOrHistory() {
  if (logMode === "history") {
    if (active) {
      stopLogs();
      $("log").innerHTML = "<div class=\"empty\">Pick From / To (date &amp; time) and click Load</div>";
      $("eof").classList.add("hidden");
    }
    return;
  }
  if (active && active.archivedOnly) {
    $("log").innerHTML = "<div class=\"empty\">This workload only exists in S3 archive — switch to History</div>";
    $("eof").classList.add("hidden");
    return;
  }
  follow();
}

async function refreshArchivedWorkloads() {
  if (!archiveInfo.enabled) {
    archivedWorkloads = [];
    return;
  }
  try {
    const d = await api("/api/archive/workloads");
    archivedWorkloads = d.workloads || [];
  } catch (e) {
    archivedWorkloads = [];
  }
}

async function loadHistory() {
  if (!active) {
    $("log").innerHTML = "<div class=\"empty\">Select a workload (live or archived)</div>";
    return;
  }
  if (!archiveInfo.enabled) {
    $("log").innerHTML = "<div class=\"empty\">S3 archive is not enabled on this deployment</div>";
    return;
  }
  const from = fromLocalInputValue($("histFrom") && $("histFrom").value);
  const to = fromLocalInputValue($("histTo") && $("histTo").value);
  if (!from || !to || !(to > from)) {
    $("log").innerHTML = "<div class=\"empty\">Choose a valid From / To date-time (within last " + retentionDays() + " days)</div>";
    return;
  }
  const min = new Date(Date.now() - retentionDays() * 24 * 60 * 60 * 1000);
  if (from < min) {
    $("log").innerHTML = "<div class=\"empty\">From is older than retention (" + retentionDays() + " days)</div>";
    return;
  }
  stopLogs();
  const ctrl = new AbortController();
  esAbort = ctrl;
  $("log").innerHTML = "";
  stickToBottom = true;
  $("eof").classList.remove("hidden");
  const eofLabel = $("eof") && $("eof").querySelector("span:nth-child(2)");
  if (eofLabel) eofLabel.textContent = "History · loading…";
  const loadBtn = $("histLoad");
  if (loadBtn) loadBtn.disabled = true;

  const pods = selectedPods();
  let url = "/api/logs/history?namespace=" + encodeURIComponent(active.namespace) +
    "&container=" + encodeURIComponent($("container").value || "") +
    "&kind=" + encodeURIComponent(active.kind || "") +
    "&workload=" + encodeURIComponent(active.name || "") +
    "&from=" + encodeURIComponent(from.toISOString()) +
    "&to=" + encodeURIComponent(to.toISOString());
  if (pods.length) {
    url += "&pods=" + encodeURIComponent(pods.join(","));
  }

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
    let got = false;
    while (true) {
      const { value, done } = await reader.read();
      if (done) break;
      got = true;
      bumpActivity();
      queueChunk(dec.decode(value, { stream: true }));
    }
    flushPending();
    if (!got) {
      $("log").innerHTML = "<div class=\"empty\">No archived logs in this date/time range</div>";
      $("eof").classList.add("hidden");
    } else {
      if (eofLabel) eofLabel.textContent = "History · end of range";
      if (stickToBottom) scrollLive();
    }
  } catch (e) {
    if (e.name === "AbortError") return;
    queueChunk("\n[history error] " + (e.message || e) + "\n");
    flushPending();
  } finally {
    if (loadBtn) loadBtn.disabled = false;
    if (esAbort === ctrl) esAbort = null;
  }
}

function scrollLive() {
  const el = $("log");
  requestAnimationFrame(() => {
    el.scrollTop = el.scrollHeight;
    requestAnimationFrame(() => { el.scrollTop = el.scrollHeight; });
  });
  $("eof").classList.remove("hidden");
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
  if (!v || v === "__all__") return (active.pods || []).slice();
  return [v];
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
  if (logMode === "history") return;
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
  const eofLabel = $("eof") && $("eof").querySelector("span:nth-child(2)");
  if (eofLabel) {
    eofLabel.textContent = usePreviousLogs ? "Previous container · end of logs" : "Live · end of logs";
  }

  const url = "/api/logs?namespace=" + encodeURIComponent(active.namespace) +
    "&pods=" + encodeURIComponent(pods.join(",")) +
    "&container=" + encodeURIComponent($("container").value || "") +
    "&kind=" + encodeURIComponent(active.kind || "") +
    "&workload=" + encodeURIComponent(active.name || "") +
    "&previous=" + (usePreviousLogs ? "1" : "0") +
    "&tail=" + (clear ? "150" : "0");

  // Previous container stream is finite — do not reconnect-follow.
  const allowReconnect = !usePreviousLogs;

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
      bumpActivity();
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
      // Keep live: reconnect without wiping history (skip for previous/finite streams).
      if (allowReconnect && active) setTimeout(() => { if (active && !esAbort && !usePreviousLogs) follow({ clear: false }); }, 1500);
    }
  }
}

$("learnMore").onclick = openAbout;
$("aboutClose").onclick = closeAbout;
$("aboutModal").onclick = (e) => { if (e.target === $("aboutModal")) closeAbout(); };
document.addEventListener("keydown", (e) => { if (e.key === "Escape") closeAbout(); });

$("q").oninput = renderList;
if ($("qProblems")) $("qProblems").oninput = renderProblems;
if ($("tabWorkloads")) $("tabWorkloads").onclick = () => setSideTab("workloads");
if ($("tabProblems")) $("tabProblems").onclick = () => setSideTab("problems");
if ($("alertBtn")) {
  $("alertBtn").onclick = () => {
    setSideTab("problems");
    if (unhealthy.length === 1) selectProblem(unhealthy[0]);
  };
}
$("pod").onchange = async () => {
  usePreviousLogs = false;
  activeProblem = null;
  const note = $("prevNote");
  if (note) note.classList.add("hidden");
  await loadContainers();
  followOrHistory();
};
$("container").onchange = () => followOrHistory();
if ($("modeLive")) $("modeLive").onclick = () => setLogMode("live");
if ($("modeHistory")) $("modeHistory").onclick = () => setLogMode("history");
if ($("histLoad")) $("histLoad").onclick = () => loadHistory();
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

async function refreshUnhealthy() {
  try {
    const d = await api("/api/unhealthy-pods");
    unhealthy = d.pods || [];
    renderProblems();
  } catch (e) {}
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
  if (me.archive) archiveInfo = me.archive;
  applyArchiveUI();
  initHistoryRangeDefaults();
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
  if (archiveInfo.enabled) await refreshArchivedWorkloads();
  mergeDisplayWorkloads();
  renderList();
  await refreshUnhealthy();
  setInterval(async () => {
    try {
      const d = await api("/api/workloads");
      workloads = d.workloads || [];
      if (logMode === "history" && archiveInfo.enabled) await refreshArchivedWorkloads();
      mergeDisplayWorkloads();
      renderList();
    } catch (e) {}
  }, 5000);
  setInterval(refreshUnhealthy, 8000);
})();
