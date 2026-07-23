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
  if (sessionGone) return;
  sessionGone = true;
  sessionStorage.setItem("lv_timeout", "1");
  stopLogs();
  fetch("/api/logout", { method: "POST" }).finally(() => {
    location.href = "/login?reason=timeout";
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
        if (me && me.error === "session_timeout") goLoginTimeout();
      } catch (_) {}
    }).catch(() => {});
  }
}

["mousemove", "keydown", "click", "scroll", "touchstart"].forEach((ev) => {
  document.addEventListener(ev, bumpActivity, { passive: true });
});
bumpActivity();

function applyTheme(theme) {
  document.documentElement.setAttribute("data-theme", theme);
  localStorage.setItem("lv_theme", theme);
  if ($("themeSel")) $("themeSel").value = theme;
}
function applyFont(size) {
  document.documentElement.style.setProperty("--font-size", size + "px");
  localStorage.setItem("lv_font", String(size));
  if ($("fontSize")) $("fontSize").value = String(size);
}
applyTheme(localStorage.getItem("lv_theme") || "dark");
applyFont(parseInt(localStorage.getItem("lv_font") || "12", 10));

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
    let timedOut = false;
    try {
      const body = await res.clone().json();
      timedOut = body && body.error === "session_timeout";
    } catch (_) {}
    if (timedOut) goLoginTimeout();
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
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

function colorizeLine(line) {
  let s = escapeHtml(line);
  s = s.replace(/^(\[[^\]]+\])/, "<span class=\"lv-pod\">$1</span>");
  s = s.replace(/(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d{3} PKT)/g, "<span class=\"lv-ts\">$1</span>");
  s = s.replace(/\b(ERROR|ERR|FATAL|CRITICAL|PANIC)\b/gi, "<span class=\"lv-err\">$1</span>");
  s = s.replace(/\b(WARN|WARNING)\b/gi, "<span class=\"lv-warn\">$1</span>");
  s = s.replace(/\b(DEBUG|TRACE)\b/gi, "<span class=\"lv-debug\">$1</span>");
  s = s.replace(/\b(INFO)\b/gi, "<span class=\"lv-info\">$1</span>");
  return s;
}

function flushPending() {
  flushTimer = null;
  if (!pendingText) return;
  const el = $("log");
  if (el.querySelector(".empty")) el.innerHTML = "";
  const parts = pendingText.split("\n");
  pendingText = parts.pop() || "";
  let html = "";
  parts.forEach((line) => { html += colorizeLine(line) + "\n"; });
  if (html) el.insertAdjacentHTML("beforeend", html);
  if (stickToBottom) scrollLive();
}

function queueChunk(text) {
  pendingText += text;
  if (!flushTimer) flushTimer = setTimeout(flushPending, 24);
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
$("themeSel").onchange = () => applyTheme($("themeSel").value);
$("fontSize").oninput = () => applyFont(parseInt($("fontSize").value, 10));
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
});

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
  }, 20000);
})();
