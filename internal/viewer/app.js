let workloads = [];
let active = null;
let esAbort = null;
let stickToBottom = true;
let pendingText = "";
let flushTimer = null;
const $ = (id) => document.getElementById(id);

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
applyFont(parseInt(localStorage.getItem("lv_font") || "13", 10));

async function api(path) {
  const res = await fetch(path);
  if (res.status === 401) { location.href = "/login"; throw new Error("auth"); }
  if (res.status === 403) { location.href = "/setup"; throw new Error("setup"); }
  if (!res.ok) throw new Error(await res.text());
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
      html += "<div class=\"item " + (isActive ? "active" : "") + "\" data-i=\"" + idx + "\">" +
        "<div class=\"name\"><span class=\"kind\">" + w.kind + "</span>" + w.name + "</div>" +
        "<div class=\"meta\">" + w.ready + "/" + w.replicas + " ready · " + w.pods.length + " pods</div></div>";
    });
  });
  $("list").innerHTML = html;
  document.querySelectorAll(".item").forEach((el) => {
    el.onclick = () => select(workloads[+el.dataset.i]);
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
  $("toggleLive").hidden = false;
  setLiveButton(true);
  $("pod").innerHTML = w.pods.length
    ? ["<option value=\"__all__\" selected>All pods (" + w.pods.length + ")</option>"]
        .concat(w.pods.map((p) => "<option value=\"" + p + "\">" + p + "</option>")).join("")
    : "<option value=\"\">no pods</option>";
  // start logs immediately; load containers in parallel
  const followP = follow();
  await Promise.all([loadContainers(), followP]);
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

function setLiveButton(streaming) {
  const btn = $("toggleLive");
  if (!btn) return;
  btn.textContent = streaming ? "Stop" : "Start";
  btn.dataset.state = streaming ? "streaming" : "stopped";
}

function stopLogs() {
  if (esAbort) { esAbort.abort(); esAbort = null; }
  if (flushTimer) { clearTimeout(flushTimer); flushTimer = null; }
  pendingText = "";
  $("eof").classList.add("hidden");
  setLiveButton(false);
}

function selectedPods() {
  if (!active) return [];
  const v = $("pod").value;
  if (v === "__all__") return active.pods.slice();
  return v ? [v] : [];
}

function flushPending() {
  flushTimer = null;
  if (!pendingText) return;
  const el = $("log");
  if (el.querySelector(".empty")) el.textContent = "";
  el.appendChild(document.createTextNode(pendingText));
  pendingText = "";
  if (stickToBottom) scrollLive();
}

function queueChunk(text) {
  pendingText += text;
  if (!flushTimer) flushTimer = setTimeout(flushPending, 32);
}

async function follow() {
  stopLogs();
  const pods = selectedPods();
  if (!active || !pods.length) {
    $("log").innerHTML = "<div class=\"empty\">No pods available for this workload</div>";
    setLiveButton(false);
    return;
  }
  const ctrl = new AbortController();
  esAbort = ctrl;
  $("log").textContent = "";
  stickToBottom = true;
  $("eof").classList.remove("hidden");
  setLiveButton(true);

  const url = "/api/logs?namespace=" + encodeURIComponent(active.namespace) +
    "&pods=" + encodeURIComponent(pods.join(",")) +
    "&container=" + encodeURIComponent($("container").value || "") +
    "&tail=100";

  try {
    const res = await fetch(url, { signal: ctrl.signal });
    if (!res.ok) {
      $("log").textContent = await res.text();
      $("eof").classList.add("hidden");
      setLiveButton(false);
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
    if (e.name !== "AbortError") $("log").textContent = String(e.message || e);
  } finally {
    if (esAbort === ctrl) {
      esAbort = null;
      setLiveButton(false);
    }
  }
}

$("q").oninput = renderList;
$("pod").onchange = async () => { await loadContainers(); await follow(); };
$("container").onchange = () => follow();
$("toggleLive").onclick = () => {
  if ($("toggleLive").dataset.state === "streaming") stopLogs();
  else follow();
};
$("themeSel").onchange = () => applyTheme($("themeSel").value);
$("fontSize").oninput = () => applyFont(parseInt($("fontSize").value, 10));
$("logout").onclick = async () => {
  await fetch("/api/logout", { method: "POST" });
  location.href = "/login";
};
$("log").addEventListener("scroll", () => {
  const el = $("log");
  const nearBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 60;
  stickToBottom = nearBottom;
  if (nearBottom) $("eof").classList.remove("hidden");
  else $("eof").classList.add("hidden");
});

(async () => {
  const me = await api("/api/me");
  if (me.needsSetup) { location.href = "/setup"; return; }
  if (!me.authenticated) { location.href = "/login"; return; }
  $("title").textContent = me.title || "Log Viewer";
  const data = await api("/api/workloads");
  workloads = data.workloads || [];
  renderList();
  // soft refresh — don't rebuild if unchanged count when streaming
  setInterval(async () => {
    try {
      const d = await api("/api/workloads");
      workloads = d.workloads || [];
      renderList();
    } catch (e) {}
  }, 20000);
})();
