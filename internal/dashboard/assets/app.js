"use strict";
const preferences = window.talaPreferences || {};
const storage = {
  getItem: (key) => preferences[key] ?? null,
  setItem: (key, value) => {
    preferences[key] = String(value);
    window.webkit?.messageHandlers?.tala?.postMessage({
      id: -1,
      action: "preference",
      data: { key, value: String(value) },
    });
  },
  removeItem: (key) => {
    delete preferences[key];
    window.webkit?.messageHandlers?.tala?.postMessage({
      id: -1,
      action: "preference",
      data: { key, value: "" },
    });
  },
};

const $ = (s) => document.querySelector(s),
  content = $("#content");
function icon(name) {
  const paths = {
    plus: '<path d="M12 5v14M5 12h14"/>',
    record:
      '<circle cx="12" cy="12" r="8"/><circle cx="12" cy="12" r="3" fill="currentColor" stroke="none"/>',
    import: '<path d="M12 15V3m-4 4 4-4 4 4M4 14v6h16v-6"/>',
    queue: '<path d="M9 6h11M9 12h11M9 18h11M4 6h.01M4 12h.01M4 18h.01"/>',
    settings:
      '<path d="M4 7h16M4 17h16"/><circle cx="9" cy="7" r="3" fill="var(--side)"/><circle cx="15" cy="17" r="3" fill="var(--side)"/>',
    pin: '<path d="m9 3 6 0-1 6 4 4v2H6v-2l4-4-1-6ZM12 15v6"/>',
    file: '<path d="M14 3H5v18h14V8l-5-5Zm0 0v5h5M8 12h8M8 16h6"/>',
    edit: '<path d="m14 5 5 5M4 20l5-1L21 7l-5-5L4 14v6Z"/>',
    link: '<path d="M10 13a5 5 0 0 0 7 .1l3-3a5 5 0 0 0-7.1-7.1l-1.7 1.7M14 11a5 5 0 0 0-7-.1l-3 3a5 5 0 0 0 7.1 7.1l1.7-1.7"/>',
    check: '<path d="m5 12 4 4L19 6"/>',
  };
  return `<svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${paths[name] || paths.file}</svg>`;
}
document.querySelectorAll("[data-icon]").forEach((node) => {
  node.innerHTML = icon(node.dataset.icon);
});
const state = {
  sessions: [],
  jobs: [],
  live: { active: false, mode: 0 },
  model: { state: "checking" },
  settings: { language: "auto" },
  dependencies: {},
};
let view = "new",
  selected = null,
  seq = 0,
  pending = new Map(),
  files = [],
  urlMeta = null,
  urlTitle = null,
  urlDraft = "",
  urlVersion = 0,
  urlRange = { start: "", end: "" },
  authDraft = null,
  authOpen = false,
  savingAuth = false,
  enqueuingFiles = false,
  enqueuingURL = false,
  retainMedia = false,
  startingRecording = false,
  mode = storage.getItem("displayMode") || "clean",
  font = Number(storage.getItem("fontSize")) || 17,
  autoScroll = true,
  pinned = false,
  historyQuery = "",
  historySort = storage.getItem("historySort") || "newest",
  historyGroup = storage.getItem("historyGroup") || "none",
  historyGeneration = 0,
  clearingQueue = false,
  sessionGeneration = 0;
const colorSchemes = [
  ["slate", "Slate", "The original blue", "#4779df", "#c4d6fb", "#263447"],
  ["graphite", "Graphite", "Quiet and neutral", "#596478", "#c9cdd5", "#2c3038"],
  ["evergreen", "Evergreen", "A little more grounded", "#23745a", "#bce0cc", "#233c33"],
  ["iris", "Iris", "Room for imagination", "#7956c5", "#d9c6f4", "#39304c"],
  ["tangerine", "Tangerine", "A brighter workspace", "#b34f22", "#f4d3b5", "#4b3227"],
  ["lagoon", "Lagoon", "Fresh and focused", "#08778d", "#b8e2e8", "#203e46"],
];
const systemAppearance = window.matchMedia?.("(prefers-color-scheme: dark)");
function applyAppearance() {
  const scheme = storage.getItem("colorScheme");
  const appearance = storage.getItem("appearance");
  document.documentElement.dataset.scheme = colorSchemes.some(([id]) => id === scheme) ? scheme : "slate";
  document.documentElement.dataset.appearance = appearance === "light" || appearance === "dark"
    ? appearance : systemAppearance?.matches ? "dark" : "light";
}
systemAppearance?.addEventListener("change", applyAppearance);
applyAppearance();
const terminal = new Set(["completed", "failed", "cancelled"]);
const jobStates = {
  waiting: "Waiting",
  downloading: "Downloading audio",
  preparing: "Preparing audio",
  transcribing: "Transcribing",
  paused: "Paused for live recording",
  completed: "Completed",
  failed: "Failed",
  cancelled: "Cancelled",
};
const escape = (s) =>
  String(s ?? "").replace(
    /[&<>"']/g,
    (c) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[
        c
      ],
  );
const date = (s) =>
  new Date(s).toLocaleDateString(undefined, { month: "short", day: "numeric" });
const clock = (n, full = false) => {
  n = Math.max(0, Math.floor(n || 0));
  const h = Math.floor(n / 3600),
    m = Math.floor(n / 60) % 60,
    s = n % 60;
  return (
    (h || full ? String(h).padStart(2, "0") + ":" : "") +
    String(m).padStart(2, "0") +
    ":" +
    String(s).padStart(2, "0")
  );
};
const duration = (n) => (n > 0 ? clock(n) : "Duration unavailable");
const label = (s) =>
  ({
    live: "Live recording",
    file: "Media file",
    url: "Media URL",
    legacy: "Legacy transcript",
  })[s] || s;
function rpc(action, data) {
  return new Promise((resolve, reject) => {
    if (!window.webkit?.messageHandlers?.tala) {
      reject(Error("Open this interface in the Tala application."));
      return;
    }
    const id = ++seq;
    pending.set(id, { resolve, reject });
    window.webkit.messageHandlers.tala.postMessage({
      id,
      action,
      data: data ?? null,
    });
  });
}
function error(e) {
  $("#error span").textContent = e.message || String(e);
  $("#error").hidden = false;
}
function toast(s) {
  $("#toast").textContent = s;
  $("#toast").style.display = "block";
  clearTimeout(toast.timer);
  toast.timer = setTimeout(() => ($("#toast").style.display = "none"), 2500);
}
async function action(name, data) {
  try {
    return await rpc(name, data);
  } catch (e) {
    error(e);
    return undefined;
  }
}
window.receive = function (event) {
  const d = event.data;
  if (event.type === "reply") {
    if (d.id === 0) {
      applySnapshot(d.result);
      return;
    }
    const p = pending.get(d.id);
    if (p) {
      pending.delete(d.id);
      d.error ? p.reject(Error(d.error)) : p.resolve(d.result);
    }
    return;
  }
  if (event.type === "model") {
    state.model = d;
    syncNavigation();
    if (view === "settings") renderSettings();
    if (view === "new") renderHomeRecording();
    return;
  }
  if (event.type === "tensorAcceleration") {
    state.tensorAcceleration = d;
    const indicator = $("#tensor-acceleration");
    if (indicator) indicator.textContent = d;
    return;
  }
  if (event.type === "settings") {
    state.settings = d;
    return;
  }
  if (event.type === "live") {
    const wasActive = state.live.active;
    state.live = d;
    syncNavigation();
    if (d.active && !wasActive) {
      openSession(d.sessionId);
    } else if (view === "new") renderHomeRecording();
    if (view === "session") updateRecordingBanner();
    return;
  }
  if (event.type === "jobs") {
    state.jobs = d || [];
    syncNavigation();
    if (view === "queue") renderQueue();
    if (view === "new") renderHomeQueue();
    return;
  }
  if (event.type === "segment") {
    if (selected?.id === d.sessionId) {
      if (!selected.segments.some((s) => s.id === d.id)) {
        selected.segments.push(d);
        selected.segments.sort((a, b) => a.start - b.start);
        insertSegment(d);
        if (
          autoScroll &&
          !document.querySelector(".segment textarea") &&
          !String(window.getSelection())
        )
          content.scrollTop = content.scrollHeight;
      }
    }
    return;
  }
  if (event.type === "correction") {
    if (selected?.id === d.ID) {
      if (d.Text === null) delete selected.corrections[d.Segment];
      else selected.corrections[d.Segment] = d.Text;
      replaceSegment(d.Segment);
    }
    return;
  }
  if (event.type === "sessions") {
    refreshSessions();
    return;
  }
  if (event.type === "files") {
    setFiles(d);
    return;
  }
  if (event.type === "cookiesFile") {
    if (d[0]) {
      const current = authDraft || state.settings.urlAuth || {};
      authDraft = { browser: usesCookieFile(current.browser) ? current.browser : "other", profile: "", cookiesFile: d[0] };
      if ($("#cookie-file")) {
        $("#cookie-browser").value = authDraft.browser;
        $("#cookie-file").value = d[0];
        $("#cookie-profile").value = "";
        syncCookieFields(authDraft);
      }
    }
    return;
  }
  if (event.type === "modelFile") {
    if ($("#model-path")) $("#model-path").value = d[0] || "";
    return;
  }
  if (event.type === "error") {
    error(d);
    return;
  }
  if (event.type === "toast") {
    toast(d);
    return;
  }
  if (event.type === "activity") {
    $("#activity").textContent = d;
    return;
  }
  if (event.type === "resync") {
    rpc("snapshot")
      .then(applySnapshot)
      .then(() => {
        if (selected) openSession(selected.id);
      })
      .catch(error);
  }
};
function applySnapshot(s) {
  if (!s) return;
  Object.assign(state, s);
  syncNavigation();
  if (!selected && view === "new") {
    const last = storage.getItem("selectedSession");
    if (last && state.sessions.some((s) => s.id === last)) {
      openSession(last);
      return;
    }
  }
  render();
}
async function refreshSessions() {
  try {
    const s = await rpc("snapshot");
    state.sessions = s.sessions || [];
    syncNavigation();
    if (view === "history") renderHistory();
    if (view === "new") renderHomeSessions();
    if (view === "session") {
      const meta = state.sessions.find((s) => s.id === selected?.id);
      if (meta) {
        selected.duration = meta.duration;
        selected.status = meta.status;
        const node = $("#session-meta");
        if (node)
          node.innerText =
            label(selected.type) +
            " · " +
            date(selected.created) +
            " · " +
            duration(selected.duration) +
            "\n" +
            selected.source + (rangeLabel(selected.range) ? "\nSelected range: " + rangeLabel(selected.range) : "");
      }
      updateRecordingBanner();
    }
  } catch (e) {
    error(e);
  }
}
function syncNavigation() {
  document.querySelectorAll("[data-nav]").forEach((b) => {
    const active = b.dataset.nav === view || (b.dataset.nav === "history" && view === "session");
    b.classList.toggle("active", active);
    b.setAttribute("aria-current", active ? "page" : "false");
  });
  $("#active-recording").hidden = !state.live.active;
  $("#queue-count").textContent =
    state.jobs.filter((j) => !terminal.has(j.state)).length || "";
}
function navigate(next) {
  sessionGeneration++;
  view = next === "import" || next === "live" ? "new" : next;
  selected = null;
  syncNavigation();
  render();
  content.scrollTop = 0;
  content.focus({ preventScroll: true });
}
function render() {
  (
    ({
      new: renderNew,
      queue: renderQueue,
      history: renderHistory,
      settings: renderSettings,
      session: renderSession,
    })[view] || renderNew
  )();
}
function page(html) {
  $("#session-tools").hidden = true;
  $("#session-tools").innerHTML = "";
  content.innerHTML = `<div class="page ${view === "new" ? "home-page" : ""}">${html}</div>`;
}
function rows(sessions) {
  return sessions
    .map(
      (s) =>
        `<button class="session-row" data-open="${s.id}" title="${escape(s.title)}"><span class="row-icon">${icon(s.type === "live" ? "record" : "file")}</span><span class="row-main"><strong>${escape(s.title)}</strong><small>${escape(label(s.type))} · ${duration(s.duration)}${s.status === "interrupted" ? " · Interrupted" : ""}</small></span><span class="date">${date(s.created)}</span></button>`,
    )
    .join("");
}
function bindRows() {
  content
    .querySelectorAll("[data-open]")
    .forEach((b) => (b.onclick = () => openSession(b.dataset.open)));
}
function renderNew() {
  page(`
    <h1 class="visually-hidden">New transcription</h1>
    <div id="home-recording"></div>
    <div class="or-divider"><span>or bring your own media</span></div>
    <section class="import-panel" aria-label="Import audio or video">
      <button class="dropzone" id="choose-files">
        <span class="upload-symbol">${icon("import")}</span>
        <strong>Drop audio or video here</strong>
        <span class="browse-label">or choose files</span>
        <small>MP3, WAV, M4A, AAC, FLAC, OGG · MP4, MOV, MKV, WEBM, AVI</small>
      </button>
      <div id="files"></div><div id="file-actions"></div>
      <div class="link-import">
        <label for="media-url" class="visually-hidden">YouTube or direct media link</label>
        <div class="url-row">
          <span class="link-symbol">${icon("link")}</span>
          <input id="media-url" type="url" placeholder="Paste a YouTube or direct media link" autocomplete="off" aria-describedby="url-error" value="${escape(urlDraft)}">
          <button id="fetch-url">Fetch details</button>
        </div>
        <p id="url-error" class="field-error" role="alert" hidden></p>
        ${signInHelper()}
        <div id="url-details"></div>
      </div>
      <p id="import-availability" class="import-availability" hidden></p>
    </section>
    <div class="workspace-overview">
      <section class="queue-preview" aria-labelledby="queue-heading">
        <div class="section-head"><h2 id="queue-heading">Processing queue</h2><button class="text-button" id="view-queue" aria-label="View full processing queue">View all</button></div>
        <div id="home-queue"></div>
      </section>
      <section class="sessions-preview" aria-labelledby="sessions-heading">
        <div class="section-head"><h2 id="sessions-heading">Recent sessions</h2><button class="text-button" id="view-history" aria-label="View all sessions">View all</button></div>
        <div id="home-sessions"></div>
      </section>
    </div>`);
  renderHomeRecording();
  renderHomeQueue();
  renderHomeSessions();
  $("#view-queue").onclick = () => navigate("queue");
  $("#view-history").onclick = () => navigate("history");
  $("#choose-files").onclick = () => action("pickFiles");
  $("#fetch-url").onclick = fetchURL;
  $("#media-url").onkeydown = (e) => {
    if (e.key === "Enter") fetchURL();
  };
  $("#media-url").oninput = (e) => {
    urlDraft = e.target.value;
    urlVersion++;
    urlMeta = null;
    urlRange = { start: "", end: "" };
    urlTitle = null;
    retainMedia = false;
    $("#url-details").innerHTML = "";
    $("#url-error").hidden = true;
    $("#media-url").setAttribute("aria-invalid", "false");
    syncURLButton();
    if ($("#sign-in-url")) $("#sign-in-url").value = signInURL();
  };
  updateImportAvailability();
  refreshDependencies();
  renderFiles();
  renderURL();
  bindSignInHelper();
}
function renderHomeRecording() {
  if (view !== "new") return;
  const live = state.live;
  $("#home-recording").innerHTML = `${modelNotice()}
    <section class="record-bar" aria-label="Live transcription">
      <div class="record-label"><span class="record-symbol ${live.active ? "is-recording" : ""}">${icon("record")}</span><h2>Live transcription</h2></div>
      <span id="home-live-clock" class="record-clock" aria-label="Recording duration">${live.active ? clock((Date.now() - new Date(live.started)) / 1000) : "00:00"}</span>
      <select id="audio-source" aria-label="Audio source" ${live.active || startingRecording ? "disabled" : ""}>
        <option value="0">Microphone + system audio</option><option value="1">Microphone only</option><option value="2">System audio only</option>
      </select>
      ${live.active ? `<button id="home-stop-recording" class="danger" ${live.state === "stopping" ? "disabled" : ""}>${live.state === "stopping" ? "Finishing…" : "Stop recording"}</button>` : `<button id="start-recording" class="primary" ${state.model.path && !startingRecording ? "" : "disabled"}>${startingRecording ? "Starting…" : "Start recording"}</button>`}
    </section>`;
  $("#audio-source").value = live.mode;
  $("#audio-source").onchange = async (e) => {
    const control = e.target;
    const previous = state.live.mode;
    control.disabled = true;
    if ($("#start-recording")) $("#start-recording").disabled = true;
    try {
      await rpc("mode", Number(control.value));
      state.live.mode = Number(control.value);
    } catch (e) {
      control.value = previous;
      error(e);
    } finally {
      renderHomeRecording();
    }
  };
  if ($("#start-recording")) $("#start-recording").onclick = async () => {
    if (startingRecording || !state.model.path) return;
    startingRecording = true;
    renderHomeRecording();
    try {
      await rpc("start");
    } catch (e) {
      error(e);
    } finally {
      startingRecording = false;
      renderHomeRecording();
    }
  };
  if ($("#home-stop-recording")) $("#home-stop-recording").onclick = async (e) => {
    e.currentTarget.disabled = true;
    await action("stop");
    renderHomeRecording();
  };
  if ($("#open-model")) $("#open-model").onclick = () => navigate("settings");
}
function formatBytes(bytes) {
  let n = Math.max(0, Number(bytes) || 0), i = 0;
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
  return `${i ? n.toFixed(1) : Math.floor(n)} ${units[i]}`;
}
function downloadProgressHTML(job) {
  const p = job.download || {}, known = p.total > 0;
  const fraction = known ? (p.status === "finished" ? 1 : Math.max(0, Math.min(.999, (p.downloaded || 0) / p.total))) : 0;
  const percent = known ? `${p.estimated ? "~" : ""}${Math.floor(fraction * 100)}%` : "Downloading…";
  const detail = [formatBytes(p.downloaded || 0) + (known ? ` / ${p.estimated ? "~" : ""}${formatBytes(p.total)}` : " downloaded")];
  if (p.speed > 0) detail.push(`${formatBytes(p.speed)}/s`);
  if (p.eta > 0) detail.push(`ETA ${clock(p.eta)}`);
  return `<span class="job-progress download-progress"><progress max="1" ${known ? `value="${fraction}"` : ""} aria-label="Download progress"></progress><span>${percent}</span></span><small class="job-timing">${detail.join(" · ")}</small>`;
}
function renderHomeQueue() {
  if (view !== "new") return;
  const container = $("#home-queue"), scrollTop = container.scrollTop || 0;
  const active = state.jobs.filter((j) => !terminal.has(j.state));
  const recent = active.length ? active : [...state.jobs].reverse();
  container.innerHTML = recent.length
    ? recent.map((j) => `<button class="queue-item" data-queue-job="${escape(j.id)}">
        <strong>${escape(j.title)}</strong>
        <span class="queue-item-meta"><span class="${j.state === "failed" ? "job-error" : ""}">${escape(jobStates[j.state] || j.state)}</span>${j.state === "transcribing" || j.state === "paused" || j.state === "completed" ? `<span>${Math.floor((j.progress || 0) * 100)}%</span>` : ""}</span>
        ${j.state === "downloading" ? downloadProgressHTML(j) : ""}
        ${j.state === "transcribing" || j.state === "paused" ? `<progress max="1" value="${j.progress || 0}" aria-label="${escape(j.title)} transcription progress"></progress>` : ""}
      </button>`).join("")
    : `<div class="queue-empty"><span class="queue-empty-icon">${icon("check")}</span><strong>Your queue is clear</strong><p>Files and links appear here while they’re transcribed.</p></div>`;
  container.scrollTop = scrollTop;
  container.querySelectorAll("[data-queue-job]").forEach((b) => {
    b.onclick = () => navigate("queue");
  });
}
function renderHomeSessions() {
  if (view !== "new") return;
  const container = $("#home-sessions"), scrollTop = container.scrollTop || 0;
  container.innerHTML = rows(state.sessions) || `<div class="sessions-empty">${icon("file")}<div><strong>Your transcripts, all together</strong><p>Start a recording or import media above.<br>Your saved sessions will appear here.</p></div></div>`;
  container.scrollTop = scrollTop;
  bindRows();
}
function modelNotice() {
  return state.model.path
    ? ""
    : `<div class="waiting-model">${escape(state.model.state === "checking" ? "Verifying transcription model…" : "Choose a transcription model to get started.")} <button class="text-button" id="open-model">Open settings</button></div>`;
}
// Recheck after users install tools outside the app; no restart is needed.
function renderDependencyChecks() {
  const container = $("#dependency-checks");
  if (!container) return;
  const report = state.dependencies;
  const rows = ["ffmpeg", "ffprobe", "yt-dlp"]
    .map((name) => {
      const tool = report.tools?.[name];
      const status = tool
        ? tool.available
          ? "Ready"
          : "Missing"
        : "Checking…";
      const detail =
        view === "settings" && tool?.available ? tool.path : status;
      return `<div class="dependency"><span>${escape(name)}</span><small>${escape(detail)}</small></div>`;
    })
    .join("");
  const workflows = [
    ["Local files", report.file],
    ["Media URLs", report.url],
  ]
    .map(([name, flow]) => {
      if (!flow) return "";
      if (flow.ready) return `<p>${name}: ready</p>`;
      return `<p>${name}: missing ${escape(flow.missing.join(", "))}.<br>Install with <code>${escape(flow.installCommand)}</code></p>`;
    })
    .join("");
  container.innerHTML = `${rows}<div class="model-status">${workflows || "Checking media tools…"}<small>Live recording works without these tools.</small></div><button id="recheck-dependencies">Check again</button>`;
  $("#recheck-dependencies").onclick = () => refreshDependencies(true);
  updateImportAvailability();
}
function updateImportAvailability() {
  const report = state.dependencies;
  if (view === "new" && $("#import-availability")) {
    const ready = report.file?.ready && report.url?.ready;
    $("#import-availability").hidden = !!ready;
    $("#import-availability").innerHTML = report.file || report.url
      ? `Some imports need media tools. <button class="text-button" id="import-settings">Set up in Settings</button>`
      : "Checking import availability…";
    if ($("#import-settings")) $("#import-settings").onclick = () => navigate("settings");
  }
  if ($("#choose-files")) $("#choose-files").disabled = !report.file?.ready;
  syncURLButton();
  if ($("#enqueue-url")) $("#enqueue-url").disabled = !report.url?.ready || enqueuingURL;
}

async function refreshDependencies(retryFiles = false) {
  const button = $("#recheck-dependencies");
  if (button) button.disabled = true;
  try {
    state.dependencies = await rpc("dependencies");
    if (view === "settings") renderDependencyChecks();
    if (view === "new") updateImportAvailability();
    if (view === "new") {
      if (retryFiles && state.dependencies.file.ready) {
        for (const file of files.filter((file) => file.error)) {
          file.pending = true;
          file.error = "";
          renderFiles();
          try {
            file.meta = await rpc("probe", file.path);
          } catch (e) {
            file.error = e.message;
          }
          file.pending = false;
        }
      }
      renderFiles();
    }
  } catch (e) {
    error(e);
  } finally {
    if ($("#recheck-dependencies")) $("#recheck-dependencies").disabled = false;
  }
}
async function setFiles(paths) {
  if (!Array.isArray(paths) || !paths.length) return;
  sessionGeneration++;
  view = "new";
  selected = null;
  const additions = [...new Set(paths)]
    .filter((path) => !files.some((file) => file.path === path))
    .map((path) => ({ path, pending: true, rangeDraft: { start: "", end: "" } }));
  files.push(...additions);
  render();
  syncNavigation();
  for (const file of additions) {
    try {
      file.meta = await rpc("probe", file.path);
    } catch (e) {
      file.error = e.message;
    }
    file.pending = false;
    if (view === "new") renderFiles();
  }
}
function renderFiles() {
  if (!$("#files")) return;
  $("#files").innerHTML = files
    .map(
      (f, i) =>
        `<div class="file-item"><div><strong>${escape(f.path.split("/").pop())}</strong><small ${f.error ? 'class="job-error"' : ""}>${escape(f.pending ? "Inspecting audio…" : f.error || `${duration(f.meta.duration)} · ${f.meta.format}`)}</small>${f.meta && !f.error ? rangeFields(`file-range-${i}`, f.rangeDraft) : ""}</div><button class="icon-button" data-remove="${i}" aria-label="Remove file" ${enqueuingFiles ? "disabled" : ""}>×</button></div>`,
    )
    .join("");
  $("#files")
    .querySelectorAll("[data-remove]")
    .forEach(
      (b) =>
        (b.onclick = () => {
          files.splice(Number(b.dataset.remove), 1);
          renderFiles();
        }),
    );
  $("#file-actions").innerHTML = files.length
    ? `<div class="import-actions"><span class="muted">${files.length} file${files.length === 1 ? "" : "s"} selected</span><button id="enqueue-files" class="primary" ${enqueuingFiles || files.some((f) => f.pending || f.error) || !state.dependencies.file?.ready ? "disabled" : ""}>${enqueuingFiles ? "Adding…" : "Add to queue"}</button></div>`
    : "";
  files.forEach((f, i) => {
    if (f.meta && !f.error) bindRange(`file-range-${i}`, f.rangeDraft, f.meta.duration);
  });
  if ($("#enqueue-files"))
    $("#enqueue-files").onclick = async () => {
      if (enqueuingFiles || files.some((f) => f.pending || f.error)) return;
      enqueuingFiles = true;
      try {
        // Validate every selection first. Remove only successfully queued files,
        // so a later error cannot cause duplicate jobs on the next attempt.
        const entries = files.map((file) => ({ file, range: readRange(file.rangeDraft, file.meta.duration) }));
        renderFiles();
        for (const { file, range } of entries) {
          await rpc("enqueue", { sources: [file.path], kind: "file", range });
          files = files.filter((f) => f !== file);
        }
        if (!files.length && view === "new") navigate("queue");
      } catch (e) {
        error(e);
      } finally {
        enqueuingFiles = false;
        if (view === "new") renderFiles();
      }
    };
}
async function fetchURL() {
  if (!state.dependencies.url?.ready || fetchURL.pending) return;
  const raw = $("#media-url").value.trim();
  if (!raw) return;
  if (!/^https?:\/\/\S+$/i.test(raw)) {
    $("#url-error").textContent = "Enter a full link starting with https:// or http://.";
    $("#url-error").hidden = false;
    $("#media-url").setAttribute("aria-invalid", "true");
    $("#media-url").focus();
    return;
  }
  const version = urlVersion;
  fetchURL.pending = true;
  syncURLButton();
  try {
    if (view === "new") $("#url-error").hidden = true;
    const result = await rpc("metadata", raw);
    if (version !== urlVersion) return;
    urlMeta = result;
    if (urlTitle === null) urlTitle = result.title || "";
    if (view === "new") renderURL();
  } catch (e) {
    if (version === urlVersion) {
      if (view === "new") {
        $("#url-error").textContent = e.message;
        $("#url-error").hidden = false;
        if (/sign.?in|login|cookies/i.test(e.message)) {
          authOpen = true;
          $("#url-sign-in").open = true;
        }
      } else error(e);
    }
  } finally {
    fetchURL.pending = false;
    syncURLButton();
  }
}
function syncURLButton() {
  const button = $("#fetch-url");
  if (!button) return;
  button.disabled = !state.dependencies.url?.ready || !!fetchURL.pending || !urlDraft.trim();
  button.textContent = fetchURL.pending ? "Fetching…" : "Fetch details";
}
function renderURL() {
  if (!urlMeta || !$("#url-details")) return;
  $("#url-details").innerHTML =
    `<div class="metadata"><label class="field"><span>Transcript name</span><input id="url-title" maxlength="200" value="${escape(urlTitle ?? urlMeta.title)}" placeholder="${escape(urlMeta.title)}" aria-describedby="url-title-help"></label><p id="url-title-help" class="range-help">Leave blank to use the source title.</p><small>${duration(urlMeta.duration)}</small>${rangeFields("url-range", urlRange)}</div><div class="import-actions"><label><input id="retain-media" type="checkbox" ${retainMedia ? "checked" : ""}>Keep downloaded media for playback</label><button class="primary" id="enqueue-url" ${state.dependencies.url?.ready && !enqueuingURL ? "" : "disabled"}>${enqueuingURL ? "Adding…" : "Add to queue"}</button></div>`;
  bindRange("url-range", urlRange, urlMeta.duration);
  $("#url-title").oninput = (e) => { urlTitle = e.target.value; };
  $("#retain-media").onchange = (e) => (retainMedia = e.target.checked);
  $("#enqueue-url").onclick = async () => {
    if (enqueuingURL || !urlMeta) return;
    const version = urlVersion;
    enqueuingURL = true;
    const b = $("#enqueue-url");
    b.disabled = true;
    try {
      await rpc("enqueue", {
        sources: [urlMeta.url],
        kind: "url",
        retain: $("#retain-media").checked,
        range: readRange(urlRange, urlMeta.duration),
        title: (urlTitle || "").trim(),
      });
      if (version === urlVersion) {
        urlMeta = null;
        urlDraft = "";
        urlTitle = null;
        urlRange = { start: "", end: "" };
        urlVersion++;
        retainMedia = false;
        if (view === "new") navigate("queue");
      }
    } catch (e) {
      error(e);
    } finally {
      enqueuingURL = false;
      b.disabled = false;
      if (view === "new") renderURL();
    }
  };
}

function parseTimestamp(raw) {
  raw = raw.trim();
  if (!raw) return 0;
  if (!/^\d+(?::\d{1,2}){0,2}(?:\.\d{1,3})?$/.test(raw))
    throw Error("Use seconds, MM:SS, or HH:MM:SS (optional milliseconds).");
  const parts = raw.split(":").map(Number);
  if (parts.slice(1).some((n) => n >= 60))
    throw Error("Minutes and seconds after a colon must be below 60.");
  const seconds = parts.reduce((total, n) => total * 60 + n, 0);
  if (!Number.isFinite(seconds) || seconds > 9223372036) throw Error("Timestamp is too large.");
  return seconds;
}
function readRange(draft, total = 0) {
  const start = parseTimestamp(draft.start), end = parseTimestamp(draft.end);
  if (draft.end.trim() && end <= start) throw Error("End time must be after start time.");
  if (total > 0 && start >= total) throw Error("Start time must be before the end of the media.");
  if (total > 0 && end > total) throw Error("End time must not exceed the media duration.");
  return { start, end };
}
function rangeFields(id, draft) {
  return `<fieldset class="time-range"><legend>Time range <span class="muted">(optional)</span></legend><div class="range-inputs"><label>Start<input id="${id}-start" type="text" placeholder="00:00" value="${escape(draft.start)}" aria-describedby="${id}-help ${id}-status" ${enqueuingFiles && id.startsWith("file-") ? "disabled" : ""}></label><label>End<input id="${id}-end" type="text" placeholder="End of media" value="${escape(draft.end)}" aria-describedby="${id}-help ${id}-status" ${enqueuingFiles && id.startsWith("file-") ? "disabled" : ""}></label></div><p id="${id}-help" class="range-help">Seconds, MM:SS, or HH:MM:SS. Blank fields use the full audio. Transcript timestamps match the original media.</p><p id="${id}-status" class="range-help" aria-live="polite"></p></fieldset>`;
}
function bindRange(id, draft, total) {
  const update = () => {
    const status = $(`#${id}-status`);
    try {
      const range = readRange(draft, total);
      status.textContent = range.start || range.end ? `Selected: ${rangeLabel(range)}` : "Entire recording";
      status.classList.remove("field-error");
    } catch (e) {
      status.textContent = e.message;
      status.classList.add("field-error");
    }
  };
  for (const side of ["start", "end"]) {
    $(`#${id}-${side}`).oninput = (e) => { draft[side] = e.target.value; update(); };
  }
  update();
}
function rangeLabel(range) {
  if (!range || (!range.start && !range.end)) return "";
  const stamp = (value) => clock(value) + (value % 1 ? (value % 1).toFixed(3).slice(1) : "");
  return `${stamp(range.start || 0)} – ${range.end ? stamp(range.end) : "end"}`;
}
function usesCookieFile(browser) { return browser === "dia" || browser === "other"; }
function signInURL() { return urlDraft.trim() || "https://www.youtube.com/"; }
function cookieModeHelp(browser) {
  if (browser === "zen") return "Zen uses its own profile. Leave the field blank for automatic selection, or open about:support in Zen and paste the Profile Folder path.";
  if (usesCookieFile(browser)) return "Sign in and export this site's cookies in Netscape cookies.txt format, then choose the file below. A URL alone does not share your signed-in session. Keep cookie files private: they grant account access.";
  return "Use a profile name or path, such as Default or Profile 1. Switch to that profile in the browser before signing in. Leave blank for the most recently used profile.";
}
function syncCookieFields(auth) {
  const fileMode = usesCookieFile(auth.browser);
  $("#cookie-profile-field").hidden = fileMode;
  $("#cookie-file-field").hidden = !fileMode;
  $("#cookie-profile").disabled = fileMode || !auth.browser || auth.browser === "safari";
  $("#cookie-mode-help").textContent = cookieModeHelp(auth.browser);
  $("#open-sign-in").disabled = !auth.browser;
}
function signInHelper() {
  const a = authDraft || state.settings.urlAuth || {};
  const browsers = [["", "Off — public links"], ["zen", "Zen"], ["dia", "Dia (cookies.txt)"], ["chrome", "Chrome"], ["firefox", "Firefox"], ["safari", "Safari"], ["edge", "Edge"], ["brave", "Brave"], ["chromium", "Chromium"], ["opera", "Opera"], ["vivaldi", "Vivaldi"], ["whale", "Whale"], ["other", "Other browser (cookies.txt)"]];
  return `<details id="url-sign-in" class="sign-in-helper" ${authOpen ? "open" : ""}>
    <summary>URL sign-in helper${state.settings.urlAuth?.browser ? ` · ${escape(state.settings.urlAuth.browser)}` : ""}</summary>
    <p>Choose the browser where you can play the media. Open the site or copy its URL into your browser, sign in, then save below and fetch details again.</p>
    <div class="auth-fields">
      <label class="field"><span>Signed-in browser</span><select id="cookie-browser">${browsers.map(([value, name]) => `<option value="${value}" ${a.browser === value || (!a.browser && !value) ? "selected" : ""}>${name}</option>`).join("")}</select></label>
      <label class="field" id="cookie-profile-field" ${usesCookieFile(a.browser) ? "hidden" : ""}><span>Browser profile (optional)</span><input id="cookie-profile" value="${escape(a.profile || "")}" placeholder="Automatic, or profile path" ${!a.browser || a.browser === "safari" ? "disabled" : ""}></label>
      <div class="field" id="cookie-file-field" ${usesCookieFile(a.browser) ? "" : "hidden"}><label for="cookie-file">Cookies.txt file</label><input id="cookie-file" value="${escape(a.cookiesFile || "")}" placeholder="Choose a local Netscape cookie export"><div class="controls"><button id="choose-cookies">Choose file…</button><button id="cookie-export-help">How to export cookies</button></div></div>
    </div>
    <p id="cookie-mode-help" class="range-help">${cookieModeHelp(a.browser)}</p>
    <label class="field sign-in-url"><span>Sign-in URL — copy and paste into any browser</span><input id="sign-in-url" readonly value="${escape(signInURL())}"></label>
    <div class="controls"><button id="open-sign-in" ${!a.browser ? "disabled" : ""}>Open site to sign in</button><button id="copy-sign-in-url">Copy URL</button><button id="save-url-auth" ${savingAuth ? "disabled" : ""}>${savingAuth ? "Saving…" : "Save sign-in settings"}</button></div>
    <p class="range-help">Tala saves only the browser/profile selection or cookie-file path. Browser cookies are read locally. Imported cookie files are used through a private temporary copy and the original is preserved.</p>
    <p class="range-help">If cookies cannot be read, close the browser and retry, or use a fresh cookies.txt export. macOS may request Keychain or file access. Access still depends on the site and installed yt-dlp.</p>
  </details>`;
}
function bindSignInHelper() {
  $("#url-sign-in").ontoggle = (e) => { authOpen = e.target.open; };
  const capture = () => {
    const browser = $("#cookie-browser").value;
    authDraft = { browser, profile: $("#cookie-profile").value.trim() };
    if (!browser || browser === "safari" || usesCookieFile(browser)) {
      authDraft.profile = "";
      $("#cookie-profile").value = "";
    }
    if (usesCookieFile(browser)) authDraft.cookiesFile = $("#cookie-file").value.trim();
    else $("#cookie-file").value = "";
    syncCookieFields(authDraft);
    return { ...authDraft };
  };
  $("#cookie-browser").onchange = capture;
  $("#cookie-profile").oninput = capture;
  $("#cookie-file").oninput = capture;
  $("#choose-cookies").onclick = () => { capture(); action("pickCookies"); };
  $("#cookie-export-help").onclick = () => action("openSignIn", { browser: "other", url: "https://github.com/yt-dlp/yt-dlp/wiki/FAQ#how-do-i-pass-cookies-to-yt-dlp" });
  $("#copy-sign-in-url").onclick = () => action("copySignInURL", signInURL());
  $("#open-sign-in").onclick = () => {
    const a = capture();
    action("openSignIn", { url: signInURL(), browser: a.browser });
  };
  $("#save-url-auth").onclick = async () => {
    if (savingAuth) return;
    const auth = capture();
    if (usesCookieFile(auth.browser) && !auth.cookiesFile) {
      error("Choose a Netscape cookies.txt export from your signed-in browser first.");
      $("#cookie-file").focus();
      return;
    }
    savingAuth = true;
    $("#save-url-auth").disabled = true;
    try {
      await rpc("urlAuth", auth);
      state.settings.urlAuth = auth;
      if ($("#url-sign-in summary")) $("#url-sign-in summary").textContent = `URL sign-in helper${auth.browser ? " · " + auth.browser : ""}`;
      urlVersion++;
      urlMeta = null;
      if (view === "new") $("#url-details").innerHTML = "";
      toast(auth.browser ? "Sign-in settings saved. Fetch details again to check access." : "Browser sign-in disabled.");
    } catch (e) {
      error(e);
    } finally {
      savingAuth = false;
      if ($("#save-url-auth")) {
        $("#save-url-auth").disabled = false;
        $("#save-url-auth").textContent = "Save sign-in settings";
      }
    }
  };
}
function renderQueue() {
  const active = state.jobs.filter((j) => !terminal.has(j.state)).length;
  page(`<h1 class="visually-hidden">Processing queue</h1>
    <section class="collection-panel" aria-label="Processing queue">
      <div class="collection-toolbar"><span class="collection-count">${active ? `${active} in progress or waiting` : `${state.jobs.length} finished import${state.jobs.length === 1 ? "" : "s"}`}</span><button id="clear-queue" ${!state.jobs.length || clearingQueue ? "disabled" : ""}>${clearingQueue ? "Clearing…" : "Clear queue"}</button></div>
      <div class="queue-list">${state.jobs.length ? state.jobs.map((j) => `
        <article class="job">
          <div class="job-head"><span class="job-symbol">${icon(j.kind === "url" ? "link" : "file")}</span><div class="job-title"><h2>${escape(j.title)}</h2><span class="job-status" data-state="${escape(j.state)}">${escape(jobStates[j.state] || j.state)}</span><small>${duration(j.duration)}${rangeLabel(j.range) ? ` · ${rangeLabel(j.range)}` : ""}</small></div>
            <div class="controls">${j.sessionId ? `<button data-open="${j.sessionId}">Open transcript</button>` : ""}${!terminal.has(j.state) ? `<button data-cancel="${j.id}">Cancel</button>` : j.state !== "completed" ? `<button data-retry="${j.id}">Retry</button>` : ""}</div>
          </div>
          ${j.state === "downloading" ? downloadProgressHTML(j) : ""}
          ${j.state === "transcribing" || j.state === "paused" || j.state === "completed" ? `<div class="job-progress"><progress max="1" value="${j.progress || 0}" aria-label="Transcription progress"></progress><span>${Math.floor((j.progress || 0) * 100)}%</span></div><small class="job-timing">${clock(j.processed)} processed · ${clock(j.elapsed)} elapsed</small>` : ""}
          ${j.error ? `<p class="job-error">${escape(j.error)}</p>` : ""}
        </article>`).join("") : `<div class="collection-empty"><span>${icon("queue")}</span><h2>Your queue is clear</h2><p>Import audio, video, or a link to get started.</p><button id="queue-import" class="primary">New transcription</button></div>`}</div>
    </section><p class="page-footnote">Files process sequentially. Imports pause during live recording.</p>`);
  bindRows();
  content.querySelectorAll("[data-cancel]").forEach((b) => (b.onclick = () => action("cancel", b.dataset.cancel)));
  content.querySelectorAll("[data-retry]").forEach((b) => (b.onclick = () => action("retry", b.dataset.retry)));
  if ($("#queue-import")) $("#queue-import").onclick = () => navigate("new");
  $("#clear-queue").onclick = clearQueue;
}
async function clearQueue() {
  if (clearingQueue || !state.jobs.length) return;
  const active = state.jobs.some((j) => !terminal.has(j.state));
  if (active && !window.confirm("Cancel all queued and active imports and clear the queue? Saved transcripts and original media will remain available.")) return;
  clearingQueue = true;
  renderQueue();
  try {
    await rpc("clearQueue");
    // Read the latest queue so imports added while this request ran stay visible.
    const snapshot = await rpc("snapshot");
    state.jobs = snapshot.jobs || [];
    syncNavigation();
    toast("Queue cleared");
  } catch (e) {
    error(e);
  } finally {
    clearingQueue = false;
    if (view === "queue") renderQueue();
    if (view === "new") renderHomeQueue();
  }
}
function renderHistory() {
  page(`<h1 class="visually-hidden">Session history</h1>
    <section class="collection-panel" aria-label="Session history">
      <div class="history-toolbar">
        <input id="history-query" type="search" class="history-search" placeholder="Search titles and transcripts" aria-label="Search session history" value="${escape(historyQuery)}">
        <label class="filter-control">Sort by<select id="history-sort"><option value="newest">Newest first</option><option value="oldest">Oldest first</option><option value="title">Title A–Z</option><option value="title-desc">Title Z–A</option><option value="longest">Longest first</option><option value="shortest">Shortest first</option></select></label>
        <label class="filter-control">Group by<select id="history-group"><option value="none">None</option><option value="day">Day</option><option value="month">Month</option><option value="type">Source</option><option value="status">Status</option></select></label>
      </div>
      <div id="history-results" aria-live="polite"></div>
    </section>`);
  $("#history-sort").value = historySort;
  $("#history-group").value = historyGroup;
  filterHistory();
  $("#history-query").oninput = (e) => {
    historyQuery = e.target.value;
    historyGeneration++;
    clearTimeout(renderHistory.timer);
    renderHistory.timer = setTimeout(filterHistory, 180);
  };
  $("#history-sort").onchange = (e) => {
    historySort = e.target.value;
    storage.setItem("historySort", historySort);
    filterHistory();
  };
  $("#history-group").onchange = (e) => {
    historyGroup = e.target.value;
    storage.setItem("historyGroup", historyGroup);
    filterHistory();
  };
}
function organizeSessions(items, sort, group) {
  const byDate = (a, b) => new Date(a.created) - new Date(b.created);
  const byTitle = (a, b) => a.title.localeCompare(b.title, undefined, { numeric: true, sensitivity: "base" });
  const compare = {
    newest: (a, b) => byDate(b, a), oldest: byDate, title: byTitle,
    "title-desc": (a, b) => byTitle(b, a),
    longest: (a, b) => (b.duration || 0) - (a.duration || 0),
    shortest: (a, b) => (a.duration || 0) - (b.duration || 0),
  }[sort] || ((a, b) => byDate(b, a));
  const sorted = [...items].sort((a, b) => compare(a, b) || byDate(b, a) || byTitle(a, b));
  const groups = new Map();
  const calendarGroup = group === "day" || group === "month";
  for (const session of sorted) {
    const created = new Date(session.created);
    const key = calendarGroup ? `${created.getFullYear()}-${String(created.getMonth() + 1).padStart(2, "0")}${group === "day" ? `-${String(created.getDate()).padStart(2, "0")}` : ""}`
      : group === "type" ? label(session.type)
      : group === "status" ? ({ completed: "Completed", processing: "In progress", recording: "Recording", interrupted: "Interrupted", cancelled: "Cancelled", failed: "Failed" }[session.status] || session.status || "Other") : "";
    if (!groups.has(key)) groups.set(key, { key, title: calendarGroup ? created.toLocaleDateString(undefined, { month: "long", ...(group === "day" ? { day: "numeric" } : {}), year: "numeric" }) : key, sessions: [] });
    groups.get(key).sessions.push(session);
  }
  const result = [...groups.values()];
  if (calendarGroup) result.sort((a, b) => sort === "oldest" ? a.key.localeCompare(b.key) : b.key.localeCompare(a.key));
  else if (group === "type" || group === "status") result.sort((a, b) => a.title.localeCompare(b.title));
  return result;
}
async function filterHistory() {
  const generation = ++historyGeneration;
  let items = state.sessions;
  const q = historyQuery.toLowerCase().trim();
  if (q) {
    try {
      const ids = await rpc("search", q);
      if (generation !== historyGeneration || view !== "history") return;
      items = items.filter((s) => ids.includes(s.id));
    } catch (e) {
      if (generation !== historyGeneration || view !== "history") return;
      error(e);
      return;
    }
  }
  if (view !== "history" || !$("#history-results")) return;
  $("#history-results").innerHTML = items.length
    ? `<p class="result-count">${items.length} session${items.length === 1 ? "" : "s"}</p>` + organizeSessions(items, historySort, historyGroup).map((g) => `<section class="history-group">${g.title ? `<h2>${escape(g.title)} <span>${g.sessions.length}</span></h2>` : ""}${rows(g.sessions)}</section>`).join("")
    : `<div class="collection-empty"><span>${icon("file")}</span><h2>${q ? "No matching sessions" : "Your transcripts, all together"}</h2><p>${q ? "Try another title or a word from your transcript." : "Start a recording or import media. Your saved sessions will appear here."}</p></div>`;
  bindRows();
}
async function openSession(id) {
  const generation = ++sessionGeneration;
  try {
    const session = await rpc("get", id);
    if (generation !== sessionGeneration) return;
    selected = session;
    selected.segments ??= [];
    selected.corrections ??= {};
    view = "session";
    storage.setItem("selectedSession", id);
    syncNavigation();
    renderSession();
    content.scrollTop = 0;
  } catch (e) {
    error(e);
  }
}
function updateRecordingBanner() {
  const target = $("#recording-banner");
  if (!target) return;
  const active = state.live.active && selected?.id === state.live.sessionId;
  target.innerHTML = active
    ? `<div class="live-notice"><span><span class="dot"></span><strong id="live-clock">${clock((Date.now() - new Date(state.live.started)) / 1000)}</strong> &nbsp; ${escape({ loading: "Loading model", recording: "Recording", stopping: "Finishing transcript" }[state.live.state] || state.live.state)}</span><button id="stop-recording" ${state.live.state === "stopping" ? "disabled" : ""}>Stop recording</button></div>`
    : "";
  if ($("#stop-recording")) $("#stop-recording").onclick = () => action("stop");
  syncSessionTools();
  if (!active && state.live.error && selected?.id === state.live.sessionId)
    error(state.live.error);
}
function renderSession() {
  if (!selected) return;
  const s = selected;
  const sourceLabel = s.mediaPath
    ? "Open media"
    : /^https?:\/\//.test(s.source)
      ? "Open media URL"
      : "Open transcript folder";
  page(
    `<div id="recording-banner"></div>
    <div class="session-heading"><h1 id="session-title">${escape(s.title)}</h1><button id="rename-session" class="icon-button" aria-label="Rename session" title="Rename session">${icon("edit")}</button></div>
    <div class="session-meta" id="session-meta">${escape(label(s.type))} · ${date(s.created)} · ${duration(s.duration)}<br>${escape(s.source)}${rangeLabel(s.range) ? `<br>Selected range: ${rangeLabel(s.range)}` : ""}</div>
    <div class="session-actions">${s.mediaPath ? '<button id="review-media">Play media</button>' : ""}<button id="open-source">${sourceLabel}</button><button id="delete-session" class="danger">Delete session</button></div>
    <div class="viewer-toolbar" role="toolbar" aria-label="Transcript display">
      <select id="display-mode" aria-label="Transcript display"><option value="clean">Text only · paragraphs</option><option value="continuous">Text only · single line</option><option value="timestamps">Timestamps</option><option value="sources">Sources + timestamps</option></select>
      <div class="font-controls"><button id="font-less" aria-label="Decrease font size">A−</button><button id="font-more" aria-label="Increase font size">A+</button></div>
      <label><input id="auto-scroll" type="checkbox" ${autoScroll ? "checked" : ""}>Auto-scroll</label>
      <input id="transcript-search" type="search" placeholder="Find in transcript" aria-label="Find in transcript">
    </div>
    <div id="transcript" class="transcript ${mode}" style="--transcript-size:${font}px"><div class="transcript-empty" id="transcript-empty">${s.status === "processing" || (state.live.active && s.id === state.live.sessionId) ? "Waiting for transcribed audio…" : "No transcript segments."}</div></div>
    <div id="no-matches" class="empty" role="status" hidden>No matching text.</div>`,
  );
  $("#session-tools").innerHTML =
    `<span id="segment-count" class="export-status"></span><div class="export-controls"><button id="copy-transcript">Copy text</button><label for="export-format">Format</label><select id="export-format"><option value="txt">TXT</option><option value="md">Markdown</option><option value="srt">SRT</option><option value="vtt">VTT</option></select><button class="primary" id="export-transcript">Export file…</button></div>`;
  $("#session-tools").hidden = false;
  s.segments.forEach(insertSegment);
  updateRecordingBanner();
  syncSessionTools();
  $("#display-mode").value = mode;
  $("#display-mode").onchange = (e) => {
    mode = e.target.value;
    storage.setItem("displayMode", mode);
    $("#transcript").className = "transcript " + mode;
  };
  $("#font-less").onclick = () => setFont(-1);
  $("#font-more").onclick = () => setFont(1);
  $("#auto-scroll").onchange = (e) => (autoScroll = e.target.checked);
  $("#transcript-search").oninput = filterSegments;
  $("#copy-transcript").onclick = () =>
    action("copy", { id: s.id, format: "txt", mode });
  $("#export-transcript").onclick = () =>
    action("export", { id: s.id, format: $("#export-format").value, mode });
  if ($("#review-media"))
    $("#review-media").onclick = () => action("play", { id: s.id, seconds: 0 });
  $("#open-source").onclick = () => action("reveal", s.id);
  $("#delete-session").onclick = async () => {
    if (
      !confirm(
        "Delete this transcript and its saved corrections? Original media, legacy text files, and model files are preserved.",
      )
    )
      return;
    try {
      await rpc("delete", s.id);
      storage.removeItem("selectedSession");
      await refreshSessions();
      navigate("new");
    } catch (e) {
      error(e);
    }
  };
  $("#rename-session").onclick = renameSession;
}
function syncSessionTools() {
  if (!$("#copy-transcript")) return;
  const count = selected?.segments.length || 0;
  $("#segment-count").textContent = `${count} segment${count === 1 ? "" : "s"}`;
  $("#copy-transcript").disabled = count === 0;
  $("#export-transcript").disabled = count === 0;
  const active = state.live.active && selected?.id === state.live.sessionId;
  $("#delete-session").disabled = active || selected?.status === "processing";
  $("#delete-session").title = active
    ? "Stop recording before deleting this session"
    : selected?.status === "processing"
      ? "Cancel processing before deleting this session"
      : "";
}
function setFont(delta) {
  font = Math.max(12, Math.min(28, font + delta));
  $("#transcript").style.setProperty("--transcript-size", font + "px");
  storage.setItem("fontSize", font);
}
function segmentNode(seg) {
  const node = document.createElement("article");
  node.className = "segment";
  node.dataset.id = seg.id;
  node.dataset.start = seg.start;
  const corrected = Object.hasOwn(selected.corrections, seg.id);
  node.classList.toggle("corrected", corrected);
  node.innerHTML = `<div class="segment-meta"><span class="source"></span>${seg.untimed ? "" : `<button class="timestamp" title="Seek media to this timestamp">${clock(seg.start)}</button>`}</div><p class="segment-text"></p><div class="segment-tools"><button class="edit-segment">Edit</button>${corrected ? '<button class="restore-segment">Restore original</button>' : ""}<span class="edited">Edited</span></div>`;
  node.querySelector(".source").textContent = seg.source;
  const text = corrected ? selected.corrections[seg.id] : seg.text;
  node.querySelector(".segment-text").textContent = text + " ";
  node.tabIndex = 0;
  const seek = node.querySelector(".timestamp");
  if (seek) {
    seek.disabled = !selected.mediaPath;
    seek.title = selected.mediaPath
      ? "Play media at " + clock(seg.start)
      : "Segment start: " + clock(seg.start);
    seek.onclick = () =>
      action("play", { id: selected.id, seconds: seg.start });
  }
  node.querySelector(".edit-segment").onclick = () => editSegment(seg, node);
  const restore = node.querySelector(".restore-segment");
  if (restore)
    restore.onclick = () =>
      action("correct", { id: selected.id, segment: seg.id, text: null });
  return node;
}
function insertSegment(seg) {
  const transcript = $("#transcript");
  if (!transcript || transcript.querySelector(`[data-id="${seg.id}"]`)) return;
  $("#transcript-empty")?.remove();
  const node = segmentNode(seg);
  const next = [...transcript.children].find(
    (n) => Number(n.dataset.start) > seg.start,
  );
  transcript.insertBefore(node, next || null);
  syncSessionTools();
  filterSegments();
}
function replaceSegment(id) {
  const old = $(`[data-id="${id}"]`),
    seg = selected.segments.find((s) => s.id === id);
  if (old && seg) old.replaceWith(segmentNode(seg));
  filterSegments();
}
function editSegment(seg, node) {
  if (node.querySelector("textarea")) return;
  const text = Object.hasOwn(selected.corrections, seg.id)
    ? selected.corrections[seg.id]
    : seg.text;
  node.innerHTML =
    '<textarea aria-label="Edit transcript segment"></textarea><div class="edit-actions"><button class="primary save-edit">Save correction</button><button class="cancel-edit">Cancel</button></div>';
  node.classList.add("editing");
  const input = node.querySelector("textarea");
  input.value = text;
  input.focus();
  const id = selected.id;
  node.querySelector(".save-edit").onclick = async () => {
    const button = node.querySelector(".save-edit");
    button.disabled = true;
    try {
      await rpc("correct", { id, segment: seg.id, text: input.value });
      if (selected?.id === id) {
        selected.corrections[seg.id] = input.value;
        replaceSegment(seg.id);
      }
    } catch (e) {
      error(e);
      button.disabled = false;
    }
  };
  node.querySelector(".cancel-edit").onclick = () => replaceSegment(seg.id);
  input.onkeydown = (e) => {
    if (e.key === "Escape") replaceSegment(seg.id);
    if (e.metaKey && e.key === "Enter")
      node.querySelector(".save-edit").click();
  };
}
function filterSegments() {
  const q = $("#transcript-search")?.value.toLowerCase() || "";
  let visible = 0;
  document.querySelectorAll(".segment").forEach((n) => {
    if (!n.querySelector("textarea"))
      n.hidden =
        !!q &&
        !n
          .querySelector(".segment-text")
          ?.textContent.toLowerCase()
          .includes(q);
    if (!n.hidden) visible++;
  });
  if ($("#no-matches")) $("#no-matches").hidden = !q || visible > 0;
}
function renameSession() {
  const title = $("#session-title");
  const original = selected.title;
  title.innerHTML =
    '<span class="inline-title"><input aria-label="Session title"><button>Save</button></span>';
  const input = title.querySelector("input");
  input.value = original;
  input.focus();
  const save = async () => {
    try {
      await rpc("rename", { id: selected.id, title: input.value });
      selected.title = input.value;
      title.textContent = input.value;
      refreshSessions();
    } catch (e) {
      error(e);
    }
  };
  title.querySelector("button").onclick = save;
  input.onkeydown = (e) => {
    if (e.key === "Enter") save();
    if (e.key === "Escape") title.textContent = original;
  };
}
function readSettings() {
  return {
    ...state.settings,
    modelPath: $("#model-path").value.trim(),
    modelDir: $("#model-dir").value.trim(),
    language: $("#language").value,
    prompt: $("#context-prompt").value,
    skipQuietImports: $("#skip-quiet-imports").checked,
    menuBarIcon: $("#menu-bar-icon").checked,
  };
}
function appearanceSettings() {
  const selected = storage.getItem("colorScheme") || "slate";
  return `<section class="settings-group appearance-settings"><div class="settings-section-head"><div><h2>Appearance</h2><p>Make a little room for your own style.</p></div><label class="filter-control">Appearance<select id="appearance"><option value="system">Follow system</option><option value="light">Light</option><option value="dark">Dark</option></select></label></div>
    <div class="theme-options" role="group" aria-label="Color scheme">${colorSchemes.map(([id, name, description, a, b, c]) => `<button class="theme-option" data-scheme="${id}" aria-pressed="${id === selected}" style="--swatch-accent:${a};--swatch-soft:${b};--swatch-dark:${c}"><span class="theme-preview" aria-hidden="true"><i></i><i></i><i></i></span><span class="theme-name">${name}<span class="theme-check">${icon("check")}</span></span><small>${description}</small></button>`).join("")}</div>
    </section>`;
}
function bindAppearanceSettings() {
  $("#appearance").value = storage.getItem("appearance") || "system";
  $("#appearance").onchange = (e) => {
    storage.setItem("appearance", e.target.value);
    applyAppearance();
  };
  content.querySelectorAll(".theme-option").forEach((b) => {
    b.onclick = () => {
      storage.setItem("colorScheme", b.dataset.scheme);
      applyAppearance();
      content.querySelectorAll(".theme-option").forEach((option) => option.setAttribute("aria-pressed", String(option === b)));
    };
  });
}
function renderSettings() {
  const m = state.model,
    s = state.settings;
  page(
    `<h1 class="visually-hidden">Settings</h1>${appearanceSettings()}<div class="settings-group"><h2>Whisper Large v3 Turbo</h2><div class="model-status"><strong>${escape({ reused: "Existing model verified", downloaded: "Model downloaded", checking: "Checking existing models…", missing: "Model required" }[m.state] || m.state)}</strong><code>${escape(m.path || m.detail || "Choose an existing model below.")}</code><small id="tensor-acceleration" role="status">${escape(state.tensorAcceleration || "Tensor acceleration status appears after Whisper loads.")}</small></div><p>Model format and SHA-256 checksum must match Whisper Large v3 Turbo.</p><label class="field"><span>Existing model path</span><input id="model-path" value="${escape(s.modelPath || m.path || "")}" placeholder="/path/to/ggml-large-v3-turbo.bin"></label><label class="field"><span>Additional model directory</span><input id="model-dir" value="${escape(s.modelDir || "")}" placeholder="Optional directory to search"></label><div class="controls"><button id="choose-model">Choose model…</button><button id="verify-model">Save and verify model</button>${m.path ? "" : '<button id="download-model">Download model (1.6 GB)</button>'}</div></div><div class="settings-group"><h2>Recognition</h2><label class="field"><span>Language</span><select id="language"><option value="auto">Automatic (English, Filipino, Taglish, other languages)</option><option value="en">English</option><option value="tl">Filipino / Tagalog</option></select></label><label class="field"><span>Vocabulary hints (optional)</span><textarea id="context-prompt" aria-describedby="vocabulary-help" placeholder="e.g. Tala, Ateneo de Manila, Kubernetes, barangay"></textarea></label><p id="vocabulary-help">Help Whisper recognize unusual names, acronyms, and technical terms by listing words you expect to hear, or adding a short description of the recording. For example: “A Taglish lecture about Kubernetes at Ateneo de Manila.” This is a recognition hint, not a replacement list or an instruction to rewrite text. It may improve spelling but does not guarantee it. Leave blank for general recordings. Applies to new audio in live, file, and URL transcriptions.</p><p>Use Automatic for mixed languages. Translation is disabled.</p><label><input id="skip-quiet-imports" type="checkbox" aria-describedby="skip-quiet-help" ${s.skipQuietImports ? "checked" : ""}> Skip nearly silent import windows</label><p id="skip-quiet-help">Speeds up file and URL imports with long pauses by skipping windows that stay nearly silent throughout. Source timestamps are preserved. Leave off for very faint recordings.</p><button id="save-settings" class="primary">Save settings</button></div><div class="settings-group"><h2>Application</h2><label><input id="menu-bar-icon" type="checkbox" ${s.menuBarIcon ? "checked" : ""}> Show Tala in the menu bar</label><p>Off by default. Enable quick recording controls in the menu bar. You can always reopen Tala from the Dock.</p></div><div class="settings-group"><h2>Media tools</h2><p>FFmpeg decodes local files. yt-dlp imports supported URLs. Required for media imports.</p><div id="dependency-checks" aria-live="polite"></div>${signInHelper()}</div><div class="settings-group"><h2>Storage</h2><p>${escape(state.output || "~/Documents/Tala/")}<br>Temporary audio is deleted after processing.</p><button id="open-storage">Open transcript folder</button></div>`,
  );
  bindAppearanceSettings();
  bindSignInHelper();
  renderDependencyChecks();
  refreshDependencies();
  $("#language").value = s.language || "auto";
  $("#context-prompt").value = s.prompt || "";
  $("#menu-bar-icon").onchange = async (event) => {
    const enabled = event.target.checked;
    try {
      await rpc("menuBarIcon", enabled);
      state.settings.menuBarIcon = enabled;
    } catch (e) {
      event.target.checked = !enabled;
      error(e);
    }
  };
  $("#choose-model").onclick = () => action("pickModel");
  $("#verify-model").onclick = async () => {
    const button = $("#verify-model");
    button.disabled = true;
    try {
      const settings = readSettings();
      await rpc("settings", settings);
      state.settings = settings;
      renderSettings();
      toast("Model verified");
    } catch (e) {
      error(e);
    } finally {
      if ($("#verify-model")) $("#verify-model").disabled = false;
    }
  };
  if ($("#download-model"))
    $("#download-model").onclick = async () => {
      const b = $("#download-model");
      b.disabled = true;
      try {
        await rpc("downloadModel");
        toast("Model ready");
        state.settings = (await rpc("snapshot")).settings;
        renderSettings();
      } catch (e) {
        error(e);
        b.disabled = false;
      }
    };
  $("#save-settings").onclick = async () => {
    const b = $("#save-settings");
    b.disabled = true;
    try {
      const settings = readSettings();
      await rpc("settings", settings);
      state.settings = settings;
      renderSettings();
      toast("Settings saved");
    } catch (e) {
      error(e);
    } finally {
      if ($("#save-settings")) $("#save-settings").disabled = false;
    }
  };
  $("#open-storage").onclick = () => action("reveal", "");
}
$("#error button").onclick = () => ($("#error").hidden = true);
document
  .querySelectorAll("[data-nav]")
  .forEach((b) => (b.onclick = () => navigate(b.dataset.nav)));
$("#active-recording").onclick = () => openSession(state.live.sessionId);
$("#pin").onclick = async () => {
  try {
    await rpc("top", !pinned);
    pinned = !pinned;
    $("#pin").setAttribute("aria-pressed", String(pinned));
  } catch (e) {
    error(e);
  }
};
// WebKit owns the inner drag destination. Resolve paths natively from its drag
// pasteboard instead of relying on nonstandard browser File.path properties.
let fileDragDepth = 0;
const isFileDrag = (event) => [...(event.dataTransfer?.types || [])].includes("Files");
function clearFileDrag() {
  fileDragDepth = 0;
  document.body.classList.remove("file-drag");
}
document.addEventListener("dragenter", (event) => {
  if (!isFileDrag(event)) return;
  event.preventDefault();
  fileDragDepth++;
  document.body.classList.add("file-drag");
});
document.addEventListener("dragover", (event) => {
  if (!isFileDrag(event)) return;
  event.preventDefault();
  event.dataTransfer.dropEffect = "copy";
});
document.addEventListener("dragleave", (event) => {
  if (isFileDrag(event) && --fileDragDepth <= 0) clearFileDrag();
});
document.addEventListener("drop", (event) => {
  const fileDrop = isFileDrag(event);
  clearFileDrag();
  if (!fileDrop) return;
  event.preventDefault();
  action("droppedFiles");
});
window.addEventListener("blur", clearFileDrag);
document.addEventListener("keydown", (e) => {
  if (e.metaKey && e.key.toLowerCase() === "n") {
    e.preventDefault();
    navigate("new");
  }
  if (e.metaKey && e.key.toLowerCase() === "f") {
    e.preventDefault();
    if (view === "session") $("#transcript-search").focus();
    else {
      navigate("history");
      $("#history-query").focus();
    }
  }
  if (e.key === "Escape") $("#error").hidden = true;
});
setInterval(() => {
  if (!state.live.active) return;
  for (const selector of ["#live-clock", "#home-live-clock"]) {
    if ($(selector)) $(selector).textContent = clock(
      (Date.now() - new Date(state.live.started)) / 1000,
    );
  }
}, 1000);
syncNavigation();
render();
if (window.webkit?.messageHandlers?.tala)
  rpc("snapshot").then(applySnapshot).catch(error);
