const $ = (id) => document.getElementById(id);
let users = [];
let allowedWorkloads = []; // "ns/Kind/name"
let loadedApps = [];

function applyTheme(theme) {
  document.documentElement.setAttribute("data-theme", theme);
  localStorage.setItem("lv_theme", theme);
  $("themeBtn").textContent = theme === "dark" ? "☀" : "☾";
}
applyTheme(localStorage.getItem("lv_theme") || "dark");
$("themeBtn").onclick = () => {
  applyTheme(document.documentElement.getAttribute("data-theme") === "dark" ? "light" : "dark");
};

async function api(path, opts = {}) {
  const res = await fetch(path, Object.assign({
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" }
  }, opts));
  const text = await res.text();
  let data = null;
  try { data = text ? JSON.parse(text) : null; } catch { data = { raw: text }; }
  if (res.status === 401) { showLogin(); throw new Error("unauthorized"); }
  if (!res.ok) throw new Error((data && data.error) || text || res.statusText);
  return data;
}

function showLogin() { $("login").classList.remove("hidden"); $("shell").classList.add("hidden"); }
function showPanel() { $("login").classList.add("hidden"); $("shell").classList.remove("hidden"); }
function selectedNS() { return [...document.querySelectorAll("#nsGrid input:checked")].map((c) => c.value); }
function mode() { const el = document.querySelector("input[name=mode]:checked"); return el ? el.value : "exclude"; }

function renderUsers() {
  $("users").innerHTML = users.map((u, i) =>
    "<tr><td><input class=\"u-name\" data-i=\"" + i + "\" value=\"" + (u.username || "") + "\"/></td>" +
    "<td><input class=\"u-pass\" data-i=\"" + i + "\" type=\"password\" placeholder=\"" + (u.username ? "(unchanged)" : "set password") + "\"/></td>" +
    "<td><input class=\"u-en\" data-i=\"" + i + "\" type=\"checkbox\" " + (u.enabled ? "checked" : "") + "/></td>" +
    "<td><button type=\"button\" class=\"secondary u-del\" data-i=\"" + i + "\">Remove</button></td></tr>"
  ).join("");
  document.querySelectorAll(".u-del").forEach((b) => {
    b.onclick = () => { users.splice(+b.dataset.i, 1); renderUsers(); };
  });
}

function renderNS(all, cfg) {
  const selected = new Set(cfg.mode === "include" ? (cfg.include || []) : (cfg.exclude || []));
  $("nsGrid").innerHTML = all.map((ns) =>
    "<label class=\"row\"><input type=\"checkbox\" value=\"" + ns + "\" " + (selected.has(ns) ? "checked" : "") + "/> " + ns + "</label>"
  ).join("");
  $("appNs").innerHTML = all.map((ns) => "<option value=\"" + ns + "\">" + ns + "</option>").join("");
}

function keyOf(w) { return w.namespace + "/" + w.kind + "/" + w.name; }

function renderAppPick() {
  const ns = $("appNs").value;
  const selected = new Set(allowedWorkloads);
  $("appPick").innerHTML = loadedApps.filter((w) => w.namespace === ns).map((w) => {
    const k = keyOf(w);
    return "<label class=\"row\"><input type=\"checkbox\" class=\"app-cb\" value=\"" + k + "\" " +
      (selected.has(k) ? "checked" : "") + "/> <span class=\"pill\" style=\"margin:0 .35rem 0 0\">" + w.kind + "</span> " + w.name + "</label>";
  }).join("") || "<div style=\"color:var(--muted)\">No apps loaded for this namespace.</div>";
  document.querySelectorAll(".app-cb").forEach((cb) => {
    cb.onchange = () => {
      if (cb.checked) {
        if (!allowedWorkloads.includes(cb.value)) allowedWorkloads.push(cb.value);
      } else {
        allowedWorkloads = allowedWorkloads.filter((x) => x !== cb.value);
      }
      updateAllowedCount();
    };
  });
  updateAllowedCount();
}

function updateAllowedCount() {
  $("allowedCount").textContent = allowedWorkloads.length
    ? ("Specific apps selected: " + allowedWorkloads.length + " (viewer shows only these)")
    : "No specific apps selected — viewer shows all apps in allowed namespaces.";
}

function fill(cfg) {
  document.querySelectorAll("input[name=mode]").forEach((r) => { r.checked = r.value === cfg.mode; });
  $("title").value = cfg.title || "Log Viewer";
  $("wlDeploy").checked = !!(cfg.workloads && cfg.workloads.deployments);
  $("wlSTS").checked = !!(cfg.workloads && cfg.workloads.statefulSets);
  $("wlDS").checked = !!(cfg.workloads && cfg.workloads.daemonSets);
  $("wlJobs").checked = !!(cfg.workloads && cfg.workloads.jobs);
  users = (cfg.users || []).map((u) => ({ username: u.username, enabled: u.enabled !== false, password: "" }));
  allowedWorkloads = (cfg.allowedWorkloads || []).slice();
  renderUsers();
  updateAllowedCount();
}

async function load() {
  const data = await api("/api/admin/namespaces");
  fill(data.config);
  renderNS(data.all || [], data.config);
  showPanel();
}

function setStatus(msg, ok) {
  const el = $("status");
  el.textContent = msg;
  el.className = "status " + (ok ? "ok" : (msg ? "err" : ""));
}

document.querySelectorAll("nav .nav").forEach((btn) => {
  btn.onclick = () => {
    document.querySelectorAll("nav .nav").forEach((b) => b.classList.remove("active"));
    btn.classList.add("active");
    document.querySelectorAll(".tab").forEach((t) => t.classList.add("hidden"));
    $("tab-" + btn.dataset.tab).classList.remove("hidden");
  };
});

$("btnLogin").onclick = async () => {
  try {
    await api("/api/admin/login", { method: "POST", body: JSON.stringify({ password: $("password").value }) });
    await load();
  } catch (e) {
    $("loginStatus").textContent = e.message;
    $("loginStatus").className = "status err";
  }
};
$("password").addEventListener("keydown", (e) => { if (e.key === "Enter") $("btnLogin").click(); });

$("btnAddUser").onclick = () => { users.push({ username: "", enabled: true, password: "" }); renderUsers(); };
$("btnAll").onclick = () => document.querySelectorAll("#nsGrid input").forEach((c) => { c.checked = true; });
$("btnNone").onclick = () => document.querySelectorAll("#nsGrid input").forEach((c) => { c.checked = false; });
$("btnReload").onclick = () => load().catch((e) => setStatus(e.message, false));

$("btnLoadApps").onclick = async () => {
  const ns = $("appNs").value;
  setStatus("Loading apps in " + ns + "…", true);
  try {
    const data = await api("/api/admin/cluster-workloads?namespace=" + encodeURIComponent(ns));
    loadedApps = data.workloads || [];
    renderAppPick();
    setStatus("Loaded " + loadedApps.length + " apps.", true);
  } catch (e) { setStatus(e.message, false); }
};
$("appNs").onchange = () => { if (loadedApps.length) renderAppPick(); };
$("btnAppsAll").onclick = () => {
  const ns = $("appNs").value;
  loadedApps.filter((w) => w.namespace === ns).forEach((w) => {
    const k = keyOf(w);
    if (!allowedWorkloads.includes(k)) allowedWorkloads.push(k);
  });
  renderAppPick();
};
$("btnAppsClear").onclick = () => {
  const ns = $("appNs").value;
  allowedWorkloads = allowedWorkloads.filter((k) => !k.startsWith(ns + "/"));
  renderAppPick();
};

$("btnSave").onclick = async () => {
  const rows = [...document.querySelectorAll("#users tr")];
  const nextUsers = rows.map((tr) => ({
    username: tr.querySelector(".u-name").value.trim(),
    password: tr.querySelector(".u-pass").value,
    enabled: tr.querySelector(".u-en").checked
  })).filter((u) => u.username);
  const m = mode();
  const selected = selectedNS();
  if (m === "include" && selected.length === 0) {
    setStatus("Include mode needs at least one namespace.", false); return;
  }
  if (nextUsers.filter((u) => u.enabled).length === 0) {
    setStatus("Create at least one enabled viewer user before saving.", false); return;
  }
  if (!$("wlDeploy").checked && !$("wlSTS").checked && !$("wlDS").checked && !$("wlJobs").checked) {
    setStatus("Select at least one workload kind.", false); return;
  }
  const body = {
    mode: m,
    include: m === "include" ? selected : [],
    exclude: m === "exclude" ? selected : [],
    title: $("title").value || "Log Viewer",
    filter: "",
    authEnabled: true,
    users: nextUsers,
    allowedWorkloads: allowedWorkloads,
    workloads: {
      deployments: $("wlDeploy").checked,
      statefulSets: $("wlSTS").checked,
      daemonSets: $("wlDS").checked,
      jobs: $("wlJobs").checked
    }
  };
  setStatus("Applying…", true);
  try {
    const res = await api("/api/admin/config", { method: "PUT", body: JSON.stringify(body) });
    setStatus(res.applied !== false ? "Applied in realtime (shared across replicas)." : ("Saved with warning: " + res.warning), res.applied !== false);
    await load();
  } catch (e) { setStatus(e.message, false); }
};

(async () => { try { await load(); } catch { showLogin(); } })();
