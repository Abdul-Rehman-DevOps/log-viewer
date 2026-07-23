const $ = (id) => document.getElementById(id);
let users = [];
let allowedWorkloads = [];
let loadedApps = [];
let branding = {
  authorName: "Abdul Rehman",
  portfolioUrl: "https://abdulrehman.cz/",
  githubUrl: "https://github.com/Abdul-Rehman-DevOps",
  repoUrl: "https://github.com/Abdul-Rehman-DevOps/log-viewer"
};

function normalizeTheme(theme) {
  if (theme === "light" || theme === "nord-light" || theme === "solarized-light") return "light";
  return "dark";
}

function applyTheme(theme) {
  theme = normalizeTheme(theme);
  document.documentElement.setAttribute("data-theme", theme);
  localStorage.setItem("lv_theme", theme);
  const btn = $("themeBtn");
  if (btn) {
    btn.textContent = theme === "dark" ? "☀" : "☾";
    btn.title = theme === "dark" ? "Switch to light" : "Switch to dark";
  }
}
applyTheme(localStorage.getItem("lv_theme") || "dark");
$("themeBtn").onclick = () => {
  const cur = document.documentElement.getAttribute("data-theme");
  applyTheme(cur === "dark" ? "light" : "dark");
};

// Keep in sync with auth.SessionTTL (8h sliding idle).
const IDLE_MS = 8 * 60 * 60 * 1000;
let idleTimer = null;
let lastPing = 0;
let sessionGone = false;

function showLogin(reason) {
  $("login").classList.remove("hidden");
  $("shell").classList.add("hidden");
  if ($("btnLogout")) $("btnLogout").classList.add("hidden");
  const msg = {
    timeout: "Session timed out due to inactivity. Please sign in again.",
    restart: "Session ended because Log Viewer restarted. Please sign in again."
  };
  const key = reason || (sessionStorage.getItem("lv_admin_timeout") === "1" ? "timeout"
    : sessionStorage.getItem("lv_admin_restart") === "1" ? "restart" : "");
  if (key && msg[key]) {
    $("loginStatus").textContent = msg[key];
    $("loginStatus").className = "status err";
    sessionStorage.removeItem("lv_admin_timeout");
    sessionStorage.removeItem("lv_admin_restart");
  }
}

function goAdminTimeout() {
  endAdminSession("timeout");
}

function goAdminRestart() {
  endAdminSession("restart");
}

function endAdminSession(reason) {
  if (sessionGone) return;
  sessionGone = true;
  if (reason === "restart") sessionStorage.setItem("lv_admin_restart", "1");
  else sessionStorage.setItem("lv_admin_timeout", "1");
  clearTimeout(idleTimer);
  fetch("/api/admin/logout", { method: "POST", credentials: "same-origin" }).finally(() => {
    showLogin(reason);
    sessionGone = false;
  });
}

function bumpActivity() {
  if (sessionGone || $("shell").classList.contains("hidden")) return;
  clearTimeout(idleTimer);
  idleTimer = setTimeout(goAdminTimeout, IDLE_MS);
  const now = Date.now();
  if (now - lastPing > 60_000) {
    lastPing = now;
    fetch("/api/admin/namespaces", { credentials: "same-origin" }).then((res) => {
      if (res.status === 401) {
        res.json().then((body) => {
          if (body && body.error === "session_restarted") goAdminRestart();
          else if (body && body.error === "session_timeout") goAdminTimeout();
          else showLogin();
        }).catch(() => showLogin());
      }
    }).catch(() => {});
  }
}

["mousemove", "keydown", "click", "scroll", "touchstart"].forEach((ev) => {
  document.addEventListener(ev, bumpActivity, { passive: true });
});

async function api(path, opts = {}) {
  const res = await fetch(path, Object.assign({
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" }
  }, opts));
  const text = await res.text();
  let data = null;
  try { data = text ? JSON.parse(text) : null; } catch { data = { raw: text }; }
  if (res.status === 401) {
    if (data && data.error === "session_restarted") goAdminRestart();
    else if (data && data.error === "session_timeout") goAdminTimeout();
    else showLogin();
    throw new Error((data && data.message) || "unauthorized");
  }
  if (!res.ok) throw new Error((data && data.error) || text || res.statusText);
  bumpActivity();
  return data;
}

function showPanel() {
  $("login").classList.add("hidden");
  $("shell").classList.remove("hidden");
  if ($("btnLogout")) $("btnLogout").classList.remove("hidden");
  sessionGone = false;
  bumpActivity();
}
function selectedNS() { return [...document.querySelectorAll("#nsGrid input:checked")].map((c) => c.value); }
function mode() { const el = document.querySelector("input[name=mode]:checked"); return el ? el.value : "exclude"; }

function escapeHtml(s) {
  return String(s || "").replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
}

function generatePassword(len) {
  const n = len || 20;
  const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789!@#$%^&*-_=+";
  const bytes = new Uint8Array(n);
  crypto.getRandomValues(bytes);
  let out = "";
  for (let i = 0; i < n; i++) out += alphabet[bytes[i] % alphabet.length];
  return out;
}

function syncToggleLabel(btn, allChecked, selectText, unselectText) {
  if (!btn) return;
  btn.textContent = allChecked ? unselectText : selectText;
  btn.dataset.mode = allChecked ? "unselect" : "select";
}

function nsAllChecked() {
  const boxes = [...document.querySelectorAll("#nsGrid input[type=checkbox]")];
  return boxes.length > 0 && boxes.every((c) => c.checked);
}

function updateNsToggle() {
  syncToggleLabel($("btnAll"), nsAllChecked(), "Select all", "Unselect all");
}

function appsAllChecked() {
  const ns = $("appNs").value;
  const loaded = loadedApps.filter((w) => w.namespace === ns);
  if (!loaded.length) return false;
  const selected = new Set(allowedWorkloads);
  return loaded.every((w) => selected.has(keyOf(w)));
}

function updateAppsToggle() {
  syncToggleLabel($("btnAppsAll"), appsAllChecked(), "Select all loaded", "Unselect all loaded");
}

function kindsAllChecked() {
  const boxes = [...document.querySelectorAll(".kind-cb")];
  return boxes.length > 0 && boxes.every((c) => c.checked);
}

function updateKindsToggle() {
  syncToggleLabel($("btnKindsAll"), kindsAllChecked(), "Select all", "Unselect all");
}

function renderUsers() {
  $("users").innerHTML = users.map((u, i) =>
    "<tr data-i=\"" + i + "\">" +
      "<td><input class=\"u-name\" value=\"" + escapeHtml(u.username || "") + "\" autocomplete=\"username\"/></td>" +
      "<td><div class=\"pass-cell\">" +
        "<input class=\"u-pass\" type=\"password\" value=\"" + escapeHtml(u.password || "") + "\" " +
          "placeholder=\"" + (u.username ? "(unchanged)" : "set password") + "\" autocomplete=\"new-password\"/>" +
        "<div class=\"pass-tools\">" +
          "<button type=\"button\" class=\"secondary icon u-gen\" title=\"Generate strong password\" aria-label=\"Generate\">⟳</button>" +
          "<button type=\"button\" class=\"secondary icon u-show\" title=\"Show / hide\" aria-label=\"Show password\">👁</button>" +
          "<button type=\"button\" class=\"secondary icon u-copy\" title=\"Copy password\" aria-label=\"Copy\">⎘</button>" +
        "</div></div></td>" +
      "<td><input class=\"u-en\" type=\"checkbox\" " + (u.enabled ? "checked" : "") + "/></td>" +
      "<td><button type=\"button\" class=\"secondary u-del\" data-i=\"" + i + "\">Remove</button></td>" +
    "</tr>"
  ).join("");

  document.querySelectorAll("#users tr").forEach((tr) => {
    const i = +tr.dataset.i;
    const pass = tr.querySelector(".u-pass");
    const showBtn = tr.querySelector(".u-show");

    tr.querySelector(".u-name").oninput = (e) => { users[i].username = e.target.value; };
    pass.oninput = (e) => { users[i].password = e.target.value; };
    tr.querySelector(".u-en").onchange = (e) => { users[i].enabled = e.target.checked; updateStats(); };

    tr.querySelector(".u-gen").onclick = () => {
      const pw = generatePassword(20);
      users[i].password = pw;
      pass.value = pw;
      pass.type = "text";
      showBtn.textContent = "🙈";
      showBtn.title = "Hide password";
    };
    showBtn.onclick = () => {
      const visible = pass.type === "text";
      pass.type = visible ? "password" : "text";
      showBtn.textContent = visible ? "👁" : "🙈";
      showBtn.title = visible ? "Show password" : "Hide password";
    };
    tr.querySelector(".u-copy").onclick = async () => {
      const val = pass.value;
      if (!val) {
        setStatus("Nothing to copy — generate or type a password first.", false);
        return;
      }
      try {
        await navigator.clipboard.writeText(val);
        setStatus("Password copied.", true);
      } catch {
        pass.select();
        document.execCommand("copy");
        setStatus("Password copied.", true);
      }
    };
    tr.querySelector(".u-del").onclick = () => {
      users.splice(i, 1);
      renderUsers();
      updateStats();
    };
  });
}

function renderNS(all, cfg) {
  const selected = new Set(cfg.mode === "include" ? (cfg.include || []) : (cfg.exclude || []));
  $("nsGrid").innerHTML = all.map((ns) =>
    "<label class=\"row\"><input type=\"checkbox\" value=\"" + escapeHtml(ns) + "\" " +
      (selected.has(ns) ? "checked" : "") + "/> " + escapeHtml(ns) + "</label>"
  ).join("");
  $("appNs").innerHTML = all.map((ns) =>
    "<option value=\"" + escapeHtml(ns) + "\">" + escapeHtml(ns) + "</option>"
  ).join("");
  updateNsToggle();
}

function keyOf(w) { return w.namespace + "/" + w.kind + "/" + w.name; }

function renderAppPick() {
  const ns = $("appNs").value;
  const selected = new Set(allowedWorkloads);
  $("appPick").innerHTML = loadedApps.filter((w) => w.namespace === ns).map((w) => {
    const k = keyOf(w);
    return "<label class=\"row\"><input type=\"checkbox\" class=\"app-cb\" value=\"" + escapeHtml(k) + "\" " +
      (selected.has(k) ? "checked" : "") + "/> <strong style=\"font-size:.72rem;opacity:.7\">" +
      escapeHtml(w.kind) + "</strong> " + escapeHtml(w.name) + "</label>";
  }).join("") || "<div style=\"color:var(--muted)\">No apps loaded for this namespace.</div>";
  document.querySelectorAll(".app-cb").forEach((cb) => {
    cb.onchange = () => {
      if (cb.checked) {
        if (!allowedWorkloads.includes(cb.value)) allowedWorkloads.push(cb.value);
      } else {
        allowedWorkloads = allowedWorkloads.filter((x) => x !== cb.value);
      }
      updateAllowedCount();
      updateStats();
      updateAppsToggle();
    };
  });
  updateAllowedCount();
  updateAppsToggle();
}

function updateAllowedCount() {
  $("allowedCount").textContent = allowedWorkloads.length
    ? ("Pinned apps: " + allowedWorkloads.length + " — live viewer shows ONLY these (not every app in selected namespaces).")
    : "No pinned apps — viewer shows all apps in allowed namespaces.";
  updateFilterBanner();
}

function updateFilterBanner() {
  const banner = $("filterBanner");
  const text = $("filterBannerText");
  if (!banner || !text) return;
  const pins = allowedWorkloads.length;
  if (pins > 0) {
    banner.style.display = "block";
    text.innerHTML = "<strong>Live viewer is limited by " + pins + " pinned apps.</strong> " +
      "Selecting more namespaces alone will not show other deployments. " +
      "Go to <em>Apps</em> → <strong>Clear all pins</strong> → <strong>Save &amp; apply now</strong> to show everything in selected namespaces.";
  } else {
    banner.style.display = "none";
    text.textContent = "";
  }
}

function updateStats() {
  const m = mode();
  const selected = selectedNS();
  $("statNs").textContent = m === "include" ? String(selected.length) : ("all − " + selected.length);
  $("statUsers").textContent = String(users.filter((u) => u.enabled !== false).length);
  $("statApps").textContent = String(allowedWorkloads.length);
  updateFilterBanner();
}

function linkBox(id, url) {
  const el = $(id);
  if (!url) { el.textContent = "—"; return; }
  el.innerHTML = "<a href=\"" + escapeHtml(url) + "\" target=\"_blank\" rel=\"noopener\">" + escapeHtml(url) + "</a>";
}

function fillAbout(cfg) {
  branding = {
    authorName: cfg.authorName || "Abdul Rehman",
    portfolioUrl: cfg.portfolioUrl || "https://abdulrehman.cz/",
    githubUrl: cfg.githubUrl || "https://github.com/Abdul-Rehman-DevOps",
    repoUrl: cfg.repoUrl || "https://github.com/Abdul-Rehman-DevOps/log-viewer"
  };
  $("authorName").textContent = branding.authorName;
  linkBox("portfolioUrl", branding.portfolioUrl);
  linkBox("githubUrl", branding.githubUrl);
  linkBox("repoUrl", branding.repoUrl);
}

function fill(cfg) {
  document.querySelectorAll("input[name=mode]").forEach((r) => { r.checked = r.value === cfg.mode; });
  $("title").value = cfg.title || "Log Viewer";
  fillAbout(cfg);
  $("wlDeploy").checked = !!(cfg.workloads && cfg.workloads.deployments);
  $("wlSTS").checked = !!(cfg.workloads && cfg.workloads.statefulSets);
  $("wlDS").checked = !!(cfg.workloads && cfg.workloads.daemonSets);
  $("wlJobs").checked = !!(cfg.workloads && cfg.workloads.jobs);
  users = (cfg.users || []).map((u) => ({ username: u.username, enabled: u.enabled !== false, password: "" }));
  allowedWorkloads = (cfg.allowedWorkloads || []).slice();
  renderUsers();
  updateAllowedCount();
  updateStats();
  updateKindsToggle();
  updateAppsToggle();
}

async function load() {
  const data = await api("/api/admin/namespaces");
  fill(data.config);
  renderNS(data.all || [], data.config);
  showPanel();
  updateStats();
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
    $("loginStatus").textContent = "";
    $("loginStatus").className = "status";
    await api("/api/admin/login", { method: "POST", body: JSON.stringify({ password: $("password").value }) });
    await load();
  } catch (e) {
    $("loginStatus").textContent = e.message;
    $("loginStatus").className = "status err";
  }
};
$("btnLogout").onclick = async () => {
  clearTimeout(idleTimer);
  sessionGone = true;
  try {
    await fetch("/api/admin/logout", { method: "POST", credentials: "same-origin" });
  } catch (_) {}
  $("password").value = "";
  $("loginStatus").textContent = "";
  $("loginStatus").className = "status";
  showLogin();
  sessionGone = false;
};
$("password").addEventListener("keydown", (e) => { if (e.key === "Enter") $("btnLogin").click(); });
$("btnAddUser").onclick = () => { users.push({ username: "", enabled: true, password: "" }); renderUsers(); updateStats(); };

$("btnAll").onclick = () => {
  const select = $("btnAll").dataset.mode !== "unselect";
  document.querySelectorAll("#nsGrid input[type=checkbox]").forEach((c) => { c.checked = select; });
  updateNsToggle();
  updateStats();
};
$("btnNone").onclick = () => {
  document.querySelectorAll("#nsGrid input[type=checkbox]").forEach((c) => { c.checked = false; });
  updateNsToggle();
  updateStats();
};
$("nsGrid").addEventListener("change", () => { updateNsToggle(); updateStats(); });
document.querySelectorAll("input[name=mode]").forEach((r) => r.addEventListener("change", updateStats));
$("btnReload").onclick = () => load().catch((e) => setStatus(e.message, false));

$("btnKindsAll").onclick = () => {
  const select = $("btnKindsAll").dataset.mode !== "unselect";
  document.querySelectorAll(".kind-cb").forEach((c) => { c.checked = select; });
  updateKindsToggle();
};
document.querySelectorAll(".kind-cb").forEach((c) => c.addEventListener("change", updateKindsToggle));

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
$("appNs").onchange = () => { if (loadedApps.length) renderAppPick(); else updateAppsToggle(); };

$("btnAppsAll").onclick = () => {
  const ns = $("appNs").value;
  const select = $("btnAppsAll").dataset.mode !== "unselect";
  const loaded = loadedApps.filter((w) => w.namespace === ns);
  if (select) {
    loaded.forEach((w) => {
      const k = keyOf(w);
      if (!allowedWorkloads.includes(k)) allowedWorkloads.push(k);
    });
  } else {
    const drop = new Set(loaded.map(keyOf));
    allowedWorkloads = allowedWorkloads.filter((k) => !drop.has(k));
  }
  renderAppPick();
  updateStats();
};
$("btnAppsClear").onclick = () => {
  const ns = $("appNs").value;
  allowedWorkloads = allowedWorkloads.filter((k) => !k.startsWith(ns + "/"));
  renderAppPick();
  updateStats();
};
$("btnAppsClearAll").onclick = () => {
  allowedWorkloads = [];
  renderAppPick();
  updateStats();
  setStatus("All pins cleared (not saved yet — click Save & apply now).", true);
};

$("btnSave").onclick = async () => {
  const rows = [...document.querySelectorAll("#users tr")];
  const nextUsers = rows.map((tr, i) => ({
    username: tr.querySelector(".u-name").value.trim(),
    password: tr.querySelector(".u-pass").value || (users[i] && users[i].password) || "",
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
  // Branding is locked server-side; still send current values for compatibility.
  const body = {
    mode: m,
    include: m === "include" ? selected : [],
    exclude: m === "exclude" ? selected : [],
    title: $("title").value || "Log Viewer",
    authorName: branding.authorName,
    portfolioUrl: branding.portfolioUrl,
    githubUrl: branding.githubUrl,
    repoUrl: branding.repoUrl,
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
    const pins = allowedWorkloads.length;
    let msg = res.applied !== false ? "Applied in realtime." : ("Saved with warning: " + res.warning);
    if (res.applied !== false) {
      if (pins > 0) {
        msg = "Applied. Live viewer shows " + pins + " pinned app(s) only inside selected namespaces.";
      } else {
        msg = "Applied. Live viewer shows all apps in selected namespaces.";
      }
    }
    setStatus(msg, res.applied !== false);
    await load();
  } catch (e) { setStatus(e.message, false); }
};

(async () => { try { await load(); } catch { showLogin(); } })();
