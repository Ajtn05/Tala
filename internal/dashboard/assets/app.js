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
    back: '<path d="m11 6-6 6 6 6M5 12h14"/>',
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
  modelCatalog: [],
  defaultModelId: "",
  modelInventory: {},
  settings: { language: "taglish" },
  dependencies: {},
  meetingSummary: {},
  meetingProviders: {},
};
let view = "new",
  selected = null,
  sessionReturnView = "history",
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
  modelChoices = { recording: "", files: "", url: "" },
  savingModelPreference = false,
  savingMeetingProvider = false,
  savingMeetingPrompts = false,
  meetingPromptDraft = null,
  changingChatGPTAccount = "",
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
const meetingDrafts = new Map();
const savingMeetingNotes = new Map();
const generatingMeetingSummaries = new Set();
const meetingSummaryNotices = new Map();
const enhancedMeetingDrafts = new Map();
const savingEnhancedNotes = new Map();
const editingEnhancedNotes = new Set();
const previewingMeetingNotes = new Set();
const notebookTabs = new Map();
const transcriptVisibility = new Map();
const enhancedNotesReady = new Set();
try {
  const drafts = JSON.parse(storage.getItem("meetingDrafts") || "{}");
  for (const [id, notes] of Object.entries(drafts)) {
    if (typeof notes === "string") meetingDrafts.set(id, notes);
  }
} catch {}
try {
  const drafts = JSON.parse(storage.getItem("enhancedMeetingDrafts") || "{}");
  for (const [id, notes] of Object.entries(drafts)) {
    if (typeof notes === "string") enhancedMeetingDrafts.set(id, notes);
  }
} catch {}
function persistEnhancedMeetingDrafts() {
  storage.setItem("enhancedMeetingDrafts", JSON.stringify(Object.fromEntries(enhancedMeetingDrafts)));
}
function persistMeetingDrafts() {
  storage.setItem("meetingDrafts", JSON.stringify(Object.fromEntries(meetingDrafts)));
}
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
    meeting: "Meeting",
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
    refreshModelPickers();
    return;
  }
  if (event.type === "modelInventory") {
    const before = state.modelInventory;
    state.modelInventory = d;
    updateLiveReviewControls();
    syncMeetingWorkspace();
    if (d.download?.id && d.download.id === before.download?.id) {
      updateModelTransfer();
    } else {
      renderModelLibrary();
      refreshModelPickers();
    }
    return;
  }
  if (event.type === "chooseRecordingModel") {
    navigate("new");
    ($("#recording-model") || $("#open-model"))?.focus();
    return;
  }
  if (event.type === "tensorAcceleration") {
    state.tensorAcceleration = d;
    const indicator = $("#tensor-acceleration");
    if (indicator) indicator.textContent = d;
    return;
  }
  if (event.type === "settings") {
    const modelChanged = d.askModel !== state.settings.askModel || d.defaultModel !== state.settings.defaultModel;
    state.settings = d;
    if (modelChanged) {
      modelChoices = { recording: "", files: "", url: "" };
      renderModelLibrary();
      refreshModelPickers();
    }
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
    updateLiveReviewControls();
    return;
  }
  if (event.type === "jobs") {
    state.jobs = d || [];
    syncNavigation();
    if (view === "queue") renderQueue();
    if (view === "new") renderHomeQueue();
    syncMeetingWorkspace();
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
          document.activeElement !== $("#meeting-notes") &&
          document.activeElement !== $("#enhanced-meeting-notes") &&
          transcriptIsVisible() &&
          !String(window.getSelection())
        ) {
          const scroll = $("#transcript-scroll");
          if (scroll) scroll.scrollTop = scroll.scrollHeight;
        }
      }
    }
    return;
  }
  if (event.type === "correction") {
    if (selected?.id === d.ID) {
      delete selected.suggestions[d.Segment];
      if (d.Text === null) delete selected.corrections[d.Segment];
      else selected.corrections[d.Segment] = d.Text;
      replaceSegment(d.Segment);
    }
    return;
  }
  if (event.type === "reviewSuggestion") {
    if (selected?.id === d.sessionId) {
      if (d.text === null || d.accepted) delete selected.suggestions[d.segmentId];
      else selected.suggestions[d.segmentId] = d.text;
      if (d.accepted) selected.corrections[d.segmentId] = d.text;
      if (!$(`.segment[data-id="${d.segmentId}"] textarea`)) replaceSegment(d.segmentId);
    }
    return;
  }
  if (event.type === "meetingSummary") {
    state.meetingSummary = d || {};
    syncMeetingWorkspace();
    updateMeetingProviderControls();
    if (view === "new") renderHomeRecording();
    return;
  }
  if (event.type === "meetingProviders") {
    state.meetingProviders = d || {};
    if (view === "settings" && $("#meeting-chatgpt-account")) {
      renderChatGPTAccounts($("#meeting-chatgpt-account").value, $("#meeting-chatgpt-model").value);
      updateMeetingProviderControls();
    }
    return;
  }
  if (event.type === "meeting") {
    if (selected?.id === d.sessionId) {
      applyMeeting(d.sessionId, d.meeting);
      syncMeetingWorkspace();
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
      openSession(last, "history");
      return;
    }
  }
  render();
}
async function refreshSessions() {
  try {
    const s = await rpc("snapshot");
    state.sessions = s.sessions || [];
    state.meetingSummary = s.meetingSummary || state.meetingSummary;
    syncNavigation();
    if (view === "history") renderHistory();
    if (view === "new") renderHomeSessions();
    if (view === "session") {
      const meta = state.sessions.find((s) => s.id === selected?.id);
      if (meta) {
        selected.duration = meta.duration;
        selected.status = meta.status;
        applyMeeting(meta.id, meta.meeting);
        const node = $("#session-meta");
        if (node) node.innerText = sessionMetaLabel(selected);
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
  content.innerHTML = `<div class="page ${view === "new" ? "home-page" : view === "session" ? "session-page" : ""}">${html}</div>`;
}
function rows(sessions) {
  return sessions
    .map(
      (s) =>
        `<button class="session-row" data-open="${s.id}" title="${escape(s.title)}"><span class="row-icon">${icon(s.type === "live" || s.type === "meeting" ? "record" : "file")}</span><span class="row-main"><strong>${escape(s.title)}</strong><small>${escape(label(s.type))} · ${duration(s.duration)}${s.status === "interrupted" ? " · Interrupted" : ""}</small></span><span class="date">${date(s.created)}</span></button>`,
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
    modelChoices.url = "";
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
  const summaryBusy = state.meetingSummary.busy || generatingMeetingSummaries.size > 0;
  $("#home-recording").innerHTML = `${modelNotice()}
    <section class="record-bar" aria-label="Live transcription">
      <div class="record-label"><span class="record-symbol ${live.active ? "is-recording" : ""}">${icon("record")}</span><h2>Live transcription</h2></div>
      <span id="home-live-clock" class="record-clock" aria-label="Recording duration">${live.active ? clock((Date.now() - new Date(live.started)) / 1000) : "00:00"}</span>
      <select id="audio-source" aria-label="Audio source" ${live.active || startingRecording ? "disabled" : ""}>
        <option value="0">Microphone + system audio</option><option value="1">Microphone only</option><option value="2">System audio only</option>
      </select>
      ${!live.active ? modelPicker("recording", startingRecording) : ""}
      ${live.active ? `<button id="home-stop-recording" class="danger" ${live.state === "stopping" ? "disabled" : ""}>${live.state === "stopping" ? "Finishing…" : "Stop recording"}</button>` : `<button id="start-recording" class="primary" ${canUseModel("recording") && !startingRecording && !summaryBusy ? "" : "disabled"}>${startingRecording ? "Starting…" : "Start recording"}</button>`}
    </section>
    ${!live.active ? `<section class="meeting-entry" aria-labelledby="meeting-entry-heading"><div><h2 id="meeting-entry-heading">Meeting notes</h2><p>Write notes while Tala captures the conversation in the background.</p></div><button id="start-meeting" ${canUseModel("recording") && !startingRecording && !summaryBusy ? "" : "disabled"}>${startingRecording ? "Starting…" : "Start meeting"}</button></section>` : ""}`;
  bindModelPicker("recording", "#start-recording", () => !startingRecording && !state.live.active && !state.meetingSummary.busy && !generatingMeetingSummaries.size);
  $("#audio-source").value = live.mode;
  $("#audio-source").onchange = async (e) => {
    const control = e.target;
    const previous = state.live.mode;
    control.disabled = true;
    if ($("#start-recording")) $("#start-recording").disabled = true;
    if ($("#start-meeting")) $("#start-meeting").disabled = true;
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
    if (startingRecording || !canUseModel("recording") || state.meetingSummary.busy || generatingMeetingSummaries.size) return;
    const modelId = selectedModel("recording");
    startingRecording = true;
    renderHomeRecording();
    try {
      await rpc("start", modelId || null);
      modelChoices.recording = "";
    } catch (e) {
      error(e);
    } finally {
      startingRecording = false;
      renderHomeRecording();
    }
  };
  if ($("#start-meeting")) $("#start-meeting").onclick = async () => {
    if (startingRecording || !canUseModel("recording") || state.live.active || state.meetingSummary.busy || generatingMeetingSummaries.size) return;
    const modelId = selectedModel("recording");
    startingRecording = true;
    renderHomeRecording();
    try {
      await rpc("startMeeting", modelId || null);
      modelChoices.recording = "";
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
function modelStatus(id) {
  return state.modelInventory.statuses?.[id] || {};
}
function installedModels() {
  return state.modelCatalog.filter((m) => modelStatus(m.id).path);
}
function preferredModelId() {
  return state.modelCatalog.find((m) => m.id === state.settings.defaultModel)?.id || state.defaultModelId;
}
function selectedModel(kind) {
  const choice = modelChoices[kind];
  if (choice && modelStatus(choice).path) return choice;
  return !state.settings.askModel && state.model.path ? state.model.id || "" : "";
}
function canUseModel(kind) {
  return state.modelCatalog.length ? !!selectedModel(kind) : !!state.model.path;
}
function modelPicker(kind, disabled = false) {
  const installed = installedModels();
  if (!installed.length) return "";
  const selected = selectedModel(kind);
  return `<label class="model-picker"><span>Model</span><select id="${kind}-model" ${disabled ? "disabled" : ""}>
    <option value="" disabled ${selected ? "" : "selected"}>Choose a model</option>
    ${installed.map((m) => `<option value="${escape(m.id)}" ${m.id === selected ? "selected" : ""}>${escape(m.use)}${m.id === state.model.id ? " (default)" : ""}</option>`).join("")}
    </select></label>`;
}
function bindModelPicker(kind, button, ready) {
  const picker = $(`#${kind}-model`);
  if (!picker) return;
  picker.onchange = (event) => {
    modelChoices[kind] = event.target.value;
    if ($(button)) $(button).disabled = !ready() || !canUseModel(kind);
    if (kind === "recording" && $("#start-meeting")) $("#start-meeting").disabled = !ready() || !canUseModel(kind);
  };
}
function refreshModelPickers() {
  if (view !== "new") return;
  renderHomeRecording();
  renderFiles();
  renderURL();
}
function modelNotice() {
  if (state.model.path || installedModels().length) return "";
  const checking = state.model.state === "checking";
  const model = state.modelCatalog.find((m) => m.id === preferredModelId());
  const message = checking ? "Verifying transcription models…" : model
    ? `Download ${model.name} (${formatBytes(model.bytes)}) to record or import audio.`
    : "Download a model to record or import audio.";
  return `<div class="waiting-model">${escape(message)} <button class="text-button" id="open-model">Set up models</button></div>`;
}
// Recheck after users install tools outside the app; no restart is needed.
function renderDependencyChecks() {
  const container = $("#dependency-checks");
  if (!container) return;
  const report = state.dependencies;
  if (report.file?.ready && report.url?.ready) {
    container.textContent = "File and link imports are ready.";
    return;
  }
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
  const recheck = report.file && report.url ? '<button id="recheck-dependencies">Check again</button>' : "";
  container.innerHTML = `${rows}<div class="model-status">${workflows || "Checking media tools…"}<small>Live recording works without these tools.</small></div>${recheck}`;
  if ($("#recheck-dependencies")) $("#recheck-dependencies").onclick = () => refreshDependencies(true);
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
  if ($("#enqueue-url")) $("#enqueue-url").disabled = !report.url?.ready || enqueuingURL || !canUseModel("url");
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
function filesReady() {
  return !enqueuingFiles && files.length > 0 && !files.some((f) => f.pending || f.error) && state.dependencies.file?.ready;
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
    ? `<div class="import-actions"><span class="muted">${files.length} file${files.length === 1 ? "" : "s"} selected</span>${modelPicker("files", enqueuingFiles)}<button id="enqueue-files" class="primary" ${!filesReady() || !canUseModel("files") ? "disabled" : ""}>${enqueuingFiles ? "Adding…" : "Add to queue"}</button></div>`
    : "";
  files.forEach((f, i) => {
    if (f.meta && !f.error) bindRange(`file-range-${i}`, f.rangeDraft, f.meta.duration);
  });
  bindModelPicker("files", "#enqueue-files", filesReady);
  if ($("#enqueue-files"))
    $("#enqueue-files").onclick = async () => {
      if (!filesReady() || !canUseModel("files")) return;
      const modelId = selectedModel("files");
      enqueuingFiles = true;
      try {
        // Validate every selection first. Remove only successfully queued files,
        // so a later error cannot cause duplicate jobs on the next attempt.
        const entries = files.map((file) => ({ file, range: readRange(file.rangeDraft, file.meta.duration) }));
        renderFiles();
        for (const { file, range } of entries) {
          await rpc("enqueue", { sources: [file.path], kind: "file", range, ...(modelId ? { modelId } : {}) });
          files = files.filter((f) => f !== file);
        }
        if (!files.length) {
          modelChoices.files = "";
          if (view === "new") navigate("queue");
        }
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
    `<div class="metadata"><label class="field"><span>Transcript name</span><input id="url-title" maxlength="200" value="${escape(urlTitle ?? urlMeta.title)}" placeholder="${escape(urlMeta.title)}" aria-describedby="url-title-help"></label><p id="url-title-help" class="range-help">Leave blank to use the source title.</p><small>${duration(urlMeta.duration)}</small>${rangeFields("url-range", urlRange)}</div><div class="import-actions"><label><input id="retain-media" type="checkbox" ${retainMedia ? "checked" : ""}>Keep downloaded media for playback</label>${modelPicker("url", enqueuingURL)}<button class="primary" id="enqueue-url" ${state.dependencies.url?.ready && !enqueuingURL && canUseModel("url") ? "" : "disabled"}>${enqueuingURL ? "Adding…" : "Add to queue"}</button></div>`;
  bindRange("url-range", urlRange, urlMeta.duration);
  $("#url-title").oninput = (e) => { urlTitle = e.target.value; };
  $("#retain-media").onchange = (e) => (retainMedia = e.target.checked);
  bindModelPicker("url", "#enqueue-url", () => state.dependencies.url?.ready && !enqueuingURL);
  $("#enqueue-url").onclick = async () => {
    if (enqueuingURL || !urlMeta || !canUseModel("url")) return;
    const modelId = selectedModel("url");
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
        ...(modelId ? { modelId } : {}),
      });
      if (version === urlVersion) {
        urlMeta = null;
        urlDraft = "";
        urlTitle = null;
        urlRange = { start: "", end: "" };
        urlVersion++;
        modelChoices.url = "";
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
async function openSession(id, returnView = view) {
  const generation = ++sessionGeneration;
  try {
    const session = await rpc("get", id);
    if (generation !== sessionGeneration) return;
    if (returnView !== "session") sessionReturnView = returnView;
    const previousMeeting = selected?.id === id ? selected.meeting : null;
    selected = session;
    applyMeeting(id, previousMeeting);
    selected.segments ??= [];
    selected.corrections ??= {};
    selected.suggestions ??= {};
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
    ? `<div class="live-notice"><span><span class="dot"></span><strong id="live-clock">${clock((Date.now() - new Date(state.live.started)) / 1000)}</strong> &nbsp; ${escape({ loading: "Loading model", recording: "Capturing conversation", stopping: "Finishing transcript" }[state.live.state] || state.live.state)}${state.live.review ? `<small class="live-review-status" role="status">${escape(state.live.review)}</small>` : ""}</span><button id="stop-recording" ${state.live.state === "stopping" ? "disabled" : ""}>Stop recording</button></div>`
    : "";
  if ($("#stop-recording")) $("#stop-recording").onclick = () => action("stop");
  syncSessionTools();
  if (!active && state.live.error && selected?.id === state.live.sessionId)
    error(state.live.error);
}
function sessionModelName(session) {
  return state.modelCatalog.find((model) => model.id === session.modelId)?.name || "";
}
function isMeetingSession(session = selected) {
  return !!session && (session.type === "meeting" || !!session.meeting);
}
function transcriptIsVisible() {
  return !!selected && (transcriptVisibility.get(selected.id) ?? !isMeetingSession());
}
function setTranscriptVisible(visible) {
  if (!selected) return;
  transcriptVisibility.set(selected.id, visible);
  syncTranscriptDrawer();
}
function syncTranscriptDrawer() {
  if (view !== "session" || !selected || !$("#transcript-drawer")) return;
  const visible = transcriptIsVisible();
  $("#transcript-drawer").hidden = !visible;
  $("#toggle-transcript").textContent = visible ? "Hide transcript" : "Show transcript";
  $("#toggle-transcript").setAttribute("aria-expanded", String(visible));
  const count = selected.segments.length;
  const live = state.live.active && state.live.sessionId === selected.id;
  $("#transcript-capture-status").textContent = live ? `Capturing in the background · ${count} segment${count === 1 ? "" : "s"}` : selected.status === "processing" ? "Transcription in progress" : `${count} transcript segment${count === 1 ? "" : "s"}`;
}
function sessionMetaLabel(session) {
  const base = `${label(session.type)} · ${date(session.created)} · ${duration(session.duration)}`;
  const source = isMeetingSession(session) ? "" : `\n${session.source}${sessionModelName(session) ? "\n" + sessionModelName(session) : ""}`;
  return base + source + (rangeLabel(session.range) ? "\nSelected range: " + rangeLabel(session.range) : "");
}
function renderSession() {
  if (!selected) return;
  const s = selected;
  if (!transcriptVisibility.has(s.id)) transcriptVisibility.set(s.id, !isMeetingSession(s));
  const sourceLabel = s.mediaPath ? "Open media" : /^https?:\/\//.test(s.source) ? "Open media URL" : "Open transcript folder";
  const notebook = `<section class="meeting-notebook" aria-label="Meeting notes">
      <div class="notebook-toolbar">
        <div class="notebook-tabs" role="tablist" aria-label="Notes version">
          <button id="your-notes-tab" role="tab" aria-controls="your-notes-panel">Your notes</button>
          <button id="enhanced-notes-tab" role="tab" aria-controls="enhanced-notes-panel">Enhanced notes</button>
        </div>
        <button id="summarize-meeting" class="primary">Enhance notes</button>
      </div>
      <p id="meeting-summary-status" class="meeting-status enhancement-status" role="status"></p>
      <div class="meeting-setup" id="meeting-summary-setup" hidden><button id="download-meeting-model">Download notes model</button><button id="manage-meeting-usage" hidden>Manage usage</button><button id="meeting-model-settings" class="text-button">Meeting notes settings</button></div>
      <button id="cancel-meeting-summary" hidden>Cancel enhancement</button>
      <div id="your-notes-panel" class="notebook-canvas" role="tabpanel" aria-labelledby="your-notes-tab">
        <div class="notebook-preview-control"><button id="preview-meeting-notes">Preview Markdown</button></div>
        <label class="visually-hidden" for="meeting-notes">Your meeting notes</label>
        <textarea id="meeting-notes" aria-describedby="meeting-notes-status" placeholder="What stands out?\n\nJot down thoughts, questions, and next steps. Markdown shortcuts include # for headings and - [ ] for tasks."></textarea>
        <div id="meeting-notes-preview" class="meeting-summary" hidden></div>
        <div class="notebook-save-row"><p id="meeting-notes-status" class="meeting-status" role="status"></p><button id="save-meeting-notes">Save notes</button></div>
      </div>
      <div id="enhanced-notes-panel" class="notebook-canvas" role="tabpanel" aria-labelledby="enhanced-notes-tab" hidden>
        <div class="enhanced-note-actions"><p id="enhanced-notes-status" class="meeting-status" role="status"></p><button id="edit-enhanced-notes">Edit enhanced notes</button><button id="preview-enhanced-notes" hidden>Preview Markdown</button><button id="discard-enhanced-notes" hidden>Discard edits</button><button id="save-enhanced-notes" hidden>Save enhanced notes</button></div>
        <div id="meeting-summary" class="meeting-summary"></div>
        <label class="visually-hidden" for="enhanced-meeting-notes">Edit enhanced meeting notes</label>
        <textarea id="enhanced-meeting-notes" hidden></textarea>
      </div>
      <div class="meeting-share"><button id="copy-meeting">Copy notes</button><button id="export-meeting">Export Markdown…</button></div>
    </section>`;
  const transcript = `<section class="meeting-transcript" aria-label="Meeting transcript">
      <div class="transcript-drawer-heading"><div><h2>Transcript</h2><p id="transcript-capture-status" role="status"></p></div><button id="toggle-transcript" aria-controls="transcript-drawer" aria-expanded="false">Show transcript</button></div>
      <div id="transcript-drawer" hidden>
        <div class="viewer-toolbar" role="toolbar" aria-label="Transcript display">
          <select id="display-mode" aria-label="Transcript display"><option value="clean">Text only · paragraphs</option><option value="continuous">Text only · single line</option><option value="timestamps">Timestamps</option><option value="sources">Sources + timestamps</option></select>
          <div class="font-controls"><button id="font-less" aria-label="Decrease font size">A−</button><button id="font-more" aria-label="Increase font size">A+</button></div>
          <label><input id="auto-scroll" type="checkbox" ${autoScroll ? "checked" : ""}>Auto-scroll</label>
          <input id="transcript-search" type="search" placeholder="Find in transcript" aria-label="Find in transcript">
        </div>
        <div id="transcript-scroll"><div id="transcript" class="transcript ${mode}" style="--transcript-size:${font}px"><div class="transcript-empty" id="transcript-empty">${s.status === "processing" || (state.live.active && s.id === state.live.sessionId) ? "Listening in the background…" : "No transcript segments."}</div></div><div id="no-matches" class="empty" role="status" hidden>No matching text.</div></div>
        <div class="transcript-tools" role="toolbar" aria-label="Transcript export"><span id="segment-count" class="export-status"></span><div class="export-controls"><button id="copy-transcript">Copy transcript</button><label for="export-format">Format</label><select id="export-format"><option value="txt">TXT</option><option value="md">Markdown</option><option value="srt">SRT</option><option value="vtt">VTT</option></select><button id="export-transcript">Export transcript…</button></div></div>
      </div>
    </section>`;
  page(`<button id="back-from-transcript" class="text-button">${icon("back")} Back</button>
    <div id="recording-banner"></div>
    <div class="session-heading"><h1 id="session-title">${escape(s.title)}</h1><button id="rename-session" class="icon-button" aria-label="Rename session" title="Rename session">${icon("edit")}</button></div>
    <div class="session-meta" id="session-meta" title="${escape(s.source + (sessionModelName(s) ? " · " + sessionModelName(s) : ""))}">${escape(sessionMetaLabel(s))}</div>
    <div class="session-actions">${s.mediaPath ? '<button id="review-media">Play media</button>' : ""}<button id="open-source">${sourceLabel}</button><button id="delete-session" class="danger">Delete session</button></div>
    <div class="meeting-layout ${isMeetingSession(s) ? "notes-first" : "transcript-first"}">${isMeetingSession(s) ? notebook + transcript : transcript + notebook}</div>`);
  s.segments.forEach(insertSegment);
  $("#back-from-transcript").onclick = () => {
    storage.removeItem("selectedSession");
    navigate(sessionReturnView);
  };
  updateRecordingBanner();
  syncSessionTools();
  $("#toggle-transcript").onclick = () => setTranscriptVisible(!transcriptIsVisible());
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
  $("#copy-transcript").onclick = () => action("copy", { id: s.id, format: "txt", mode });
  $("#export-transcript").onclick = () => action("export", { id: s.id, format: $("#export-format").value, mode });
  if ($("#review-media")) $("#review-media").onclick = () => action("play", { id: s.id, seconds: 0 });
  $("#open-source").onclick = () => action("reveal", s.id);
  $("#delete-session").onclick = async () => {
    if (!window.confirm("Delete this transcript, original notes, enhanced notes, and saved corrections? Original media, legacy text files, and model files are preserved.")) return;
    try {
      await rpc("delete", s.id);
      storage.removeItem("selectedSession");
      meetingDrafts.delete(s.id);
      enhancedMeetingDrafts.delete(s.id);
      previewingMeetingNotes.delete(s.id);
      persistMeetingDrafts();
      persistEnhancedMeetingDrafts();
      await refreshSessions();
      navigate("new");
    } catch (e) { error(e); }
  };
  $("#rename-session").onclick = renameSession;
  bindMeetingWorkspace();
}
function applyMeeting(id, meeting) {
  if (!meeting || selected?.id !== id) return false;
  if ((meeting.revision || 0) < (selected.meeting?.revision || 0)) return false;
  selected.meeting = meeting;
  return true;
}
function meetingNotes(id = selected?.id) {
  return meetingDrafts.has(id) ? meetingDrafts.get(id) : selected?.id === id ? selected.meeting?.notes || "" : "";
}
function meetingNotesDirty() {
  return !!selected && meetingNotes() !== (selected.meeting?.notes || "");
}
function notebookTab(id = selected?.id) {
  return notebookTabs.get(id) || "notes";
}
function enhancedMeetingNotes(id = selected?.id) {
  return enhancedMeetingDrafts.has(id) ? enhancedMeetingDrafts.get(id) : selected?.id === id ? selected.meeting?.summary || "" : "";
}
function enhancedNotesDirty() {
  return !!selected && enhancedMeetingNotes() !== (selected.meeting?.summary || "");
}
function selectNotebookTab(tab) {
  if (!selected) return;
  notebookTabs.set(selected.id, tab);
  if (tab === "enhanced" && enhancedNotesReady.delete(selected.id)) meetingSummaryNotices.delete(selected.id);
  if (tab === "enhanced" && enhancedMeetingDrafts.has(selected.id)) editingEnhancedNotes.add(selected.id);
  syncMeetingWorkspace();
}
function bindMeetingWorkspace() {
  const id = selected.id;
  const notes = $("#meeting-notes"), enhanced = $("#enhanced-meeting-notes");
  notes.value = meetingNotes();
  enhanced.value = enhancedMeetingNotes();
  notes.oninput = (event) => {
    meetingDrafts.set(id, event.target.value);
    persistMeetingDrafts();
    syncMeetingWorkspace();
  };
  enhanced.oninput = (event) => {
    enhancedMeetingDrafts.set(id, event.target.value);
    persistEnhancedMeetingDrafts();
    syncMeetingWorkspace();
  };
  notes.onkeydown = (event) => {
    if ((event.metaKey || event.ctrlKey) && event.key === "Enter") {
      event.preventDefault();
      saveMeetingNotes().catch(error);
    }
  };
  enhanced.onkeydown = (event) => {
    if ((event.metaKey || event.ctrlKey) && event.key === "Enter") {
      event.preventDefault();
      saveEnhancedMeetingNotes().catch(error);
    }
  };
  for (const [tab, selector] of [["notes", "#your-notes-tab"], ["enhanced", "#enhanced-notes-tab"]]) {
    $(selector).onclick = () => selectNotebookTab(tab);
    $(selector).onkeydown = (event) => {
      if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
      event.preventDefault();
      const next = event.key === "Home" ? "notes" : event.key === "End" ? "enhanced" : tab === "notes" ? "enhanced" : "notes";
      selectNotebookTab(next);
      $(next === "notes" ? "#your-notes-tab" : "#enhanced-notes-tab").focus();
    };
  }
  $("#save-meeting-notes").onclick = () => saveMeetingNotes().catch(error);
  $("#save-enhanced-notes").onclick = () => saveEnhancedMeetingNotes().catch(error);
  $("#preview-meeting-notes").onclick = () => {
    const editing = previewingMeetingNotes.delete(id);
    if (!editing) previewingMeetingNotes.add(id);
    syncMeetingWorkspace();
    if (editing) notes.focus();
  };
  $("#meeting-notes-preview").onclick = openNotebookLink;
  $("#meeting-summary").onclick = openNotebookLink;
  $("#edit-enhanced-notes").onclick = () => {
    editingEnhancedNotes.add(id);
    syncMeetingWorkspace();
    enhanced.focus();
  };
  $("#preview-enhanced-notes").onclick = () => {
    editingEnhancedNotes.delete(id);
    syncMeetingWorkspace();
    $("#edit-enhanced-notes").focus();
  };
  $("#discard-enhanced-notes").onclick = () => {
    if (!window.confirm("Discard your unsaved enhanced edits? The saved enhanced notes and your original notes will stay available.")) return;
    enhancedMeetingDrafts.delete(id);
    persistEnhancedMeetingDrafts();
    editingEnhancedNotes.delete(id);
    syncMeetingWorkspace();
  };
  $("#summarize-meeting").onclick = summarizeMeeting;
  $("#cancel-meeting-summary").onclick = async (event) => {
    const button = event?.currentTarget || $("#cancel-meeting-summary");
    button.disabled = true;
    try {
      await rpc("cancelMeetingSummary", id);
      meetingSummaryNotices.set(id, "Enhancement canceled. Your notes and transcript are saved.");
    } catch (e) { error(e); }
    finally { button.disabled = false; syncMeetingWorkspace(); }
  };
  $("#download-meeting-model").onclick = async () => {
    const modelId = state.meetingSummary.modelId;
    if (!modelId) return;
    await changeInstalledModel($("#download-meeting-model"), "downloadModel", modelId);
    syncMeetingWorkspace();
  };
  $("#meeting-model-settings").onclick = () => {
    navigate("settings");
    $("#meeting-provider")?.focus();
  };
  $("#manage-meeting-usage").onclick = () => action("manageChatGPTUsage");
  const share = async (name) => {
    const tab = notebookTab(id);
    try {
      if (tab === "enhanced") await saveEnhancedMeetingNotes();
      else await saveMeetingNotes();
      if (selected?.id !== id) return;
      if (tab === "enhanced" ? enhancedNotesDirty() : meetingNotesDirty()) throw Error("Your notes changed while saving. Save the latest edits before exporting.");
      await rpc(name, { id, format: "md", mode: tab === "enhanced" ? "meeting" : "meeting-original" });
    } catch (e) { error(e); }
  };
  $("#copy-meeting").onclick = () => share("copy");
  $("#export-meeting").onclick = () => share("export");
  syncMeetingWorkspace();
}
async function saveEnhancedMeetingNotes() {
  if (!selected) return;
  const id = selected.id, previous = savingEnhancedNotes.get(id);
  if (previous) return previous;
  if (generatingMeetingSummaries.has(id) || (state.meetingSummary.busy && state.meetingSummary.sessionId === id)) return;
  if (!enhancedNotesDirty()) return selected.meeting;
  const text = enhancedMeetingNotes(), before = selected.meeting;
  const saving = (async () => {
    try {
      const meeting = await rpc("saveEnhancedNotes", { id, text });
      if (enhancedMeetingDrafts.get(id) === text) {
        enhancedMeetingDrafts.delete(id);
        persistEnhancedMeetingDrafts();
      }
      applyMeeting(id, meeting || { ...before, summary: text });
      if (selected?.id === id && !enhancedNotesDirty()) editingEnhancedNotes.delete(id);
      return meeting;
    } finally {
      savingEnhancedNotes.delete(id);
      if (selected?.id === id) syncMeetingWorkspace();
    }
  })();
  savingEnhancedNotes.set(id, saving);
  syncMeetingWorkspace();
  return saving;
}
async function saveMeetingNotes() {
  if (!selected) return;
  const id = selected.id;
  const previous = savingMeetingNotes.get(id);
  if (previous) return previous;
  if (!meetingNotesDirty()) return selected.meeting;
  const notes = meetingNotes(), before = selected.meeting;
  const saving = (async () => {
    try {
      const meeting = await rpc("saveMeetingNotes", { id, notes });
      if (meetingDrafts.get(id) === notes) {
        meetingDrafts.delete(id);
        persistMeetingDrafts();
      }
      applyMeeting(id, meeting || { ...before, notes });
      return meeting;
    } finally {
      savingMeetingNotes.delete(id);
      if (selected?.id === id) syncMeetingWorkspace();
    }
  })();
  savingMeetingNotes.set(id, saving);
  syncMeetingWorkspace();
  return saving;
}
async function summarizeMeeting() {
  if (!selected) return;
  const id = selected.id;
  if (enhancedNotesDirty() || savingEnhancedNotes.has(id) || generatingMeetingSummaries.size || state.meetingSummary.busy || !state.meetingSummary.ready || state.live.active || selected.status === "processing" || state.modelInventory.busy || state.jobs.some((job) => ["preparing", "transcribing"].includes(job.state))) return;
  const { provider, providerName, modelId, account, accountLabel } = state.meetingSummary;
  if (provider) {
    const usage = provider === "chatgpt" ? `This uses the ChatGPT plan or credits for ${accountLabel}.` : "This uses your API quota and may incur charges." +
      (provider === "gemini" ? " Tala may retry if Gemini reports the service is unavailable." : "");
    if (!window.confirm(`Enhance notes with ${providerName} (${modelId})? Tala will send this meeting’s notes and transcript text to ${providerName}. ${usage}`)) return;
  }
  generatingMeetingSummaries.add(id);
  meetingSummaryNotices.delete(id);
  enhancedNotesReady.delete(id);
  syncMeetingWorkspace();
  try {
    await saveMeetingNotes();
    if (selected?.id !== id) return;
    if (meetingNotesDirty()) throw Error("Your notes changed while saving. Save the latest notes, then enhance again.");
    const sourceAtRequest = meetingNotes();
    const meeting = await rpc("summarizeMeeting", { id, provider: provider || "", model: provider ? modelId : "", account: account || "" });
    applyMeeting(id, meeting);
    if (selected?.id === id && meeting?.summary) {
      const typing = document.activeElement === $("#meeting-notes");
      if (!typing && !meetingNotesDirty() && meetingNotes() === sourceAtRequest) {
        notebookTabs.set(id, "enhanced");
        editingEnhancedNotes.delete(id);
      } else {
        enhancedNotesReady.add(id);
        meetingSummaryNotices.set(id, meetingNotesDirty() ? "Enhanced notes are ready. Your newer original draft is still here." : meetingNotes() !== sourceAtRequest || selected.meeting?.summaryStale ? "Enhanced notes are ready. Your notes changed. Re-enhance to include the latest details." : "Enhanced notes are ready. Open Enhanced notes when you’re ready.");
      }
    }
  } catch (e) {
    if (/(context cancel+ed|summary cancel+ed)/i.test(e.message)) meetingSummaryNotices.set(id, "Enhancement canceled. Your notes and transcript are saved.");
    else if (e.message === "ChatGPT plan usage limit reached") meetingSummaryNotices.delete(id);
    else meetingSummaryNotices.set(id, `Enhancement failed. ${e.message}`);
  } finally {
    generatingMeetingSummaries.delete(id);
    syncMeetingWorkspace();
    if (view === "new") renderHomeRecording();
  }
}
const notebookMarkdown = globalThis.markdownit({ html: false, breaks: true });
notebookMarkdown.validateLink = (url) => {
  if (!/^https?:\/\//i.test(url)) return false;
  try {
    const parsed = new URL(url);
    return !parsed.username && !parsed.password;
  } catch {
    return false;
  }
};
// The session title is the page heading. Notes start one level below it.
for (const rule of ["heading_open", "heading_close"]) {
  notebookMarkdown.renderer.rules[rule] = (tokens, index, options, _env, renderer) => {
    const token = tokens[index];
    token.tag = `h${Math.min(6, Number(token.tag.slice(1)) + 1)}`;
    return renderer.renderToken(tokens, index, options);
  };
}
// Display the image description without loading a resource from the notes.
notebookMarkdown.renderer.rules.image = (tokens, index, options, env, renderer) =>
  escape(renderer.renderInlineAsText(tokens[index].children, options, env));
notebookMarkdown.core.ruler.after("text_join", "notebook_tasks", ({ tokens }) => {
  for (let i = 2; i < tokens.length; i++) {
    const item = tokens[i - 2], paragraph = tokens[i - 1], inline = tokens[i];
    if (item.type !== "list_item_open" || paragraph.type !== "paragraph_open" || inline.type !== "inline") continue;
    const marker = /^\[([ xX])\][ \t]+/.exec(inline.content);
    const first = inline.children[0];
    if (!marker || first?.type !== "text" || !first.content.startsWith(marker[0])) continue;
    const checkbox = new globalThis.markdownit.Token("notebook_task_checkbox", "input", 0);
    checkbox.meta = { checked: marker[1] !== " " };
    first.content = first.content.slice(marker[0].length);
    inline.children.unshift(checkbox);
    item.attrSet("class", "notebook-task");
  }
});
notebookMarkdown.renderer.rules.notebook_task_checkbox = (tokens, index) => tokens[index].meta.checked
  ? '<input type="checkbox" checked disabled aria-label="Completed task"> '
  : '<input type="checkbox" disabled aria-label="Open task"> ';

function openNotebookLink(event) {
  const link = event.target.closest("a[href]");
  if (!link) return;
  event.preventDefault();
  const url = link.getAttribute("href");
  if (notebookMarkdown.validateLink(url)) return action("openSignIn", { url, browser: "other" });
}
function showNotebookPreview(node, source, placeholder) {
  if (node.dataset.source === source) return;
  node.dataset.source = source;
  const empty = !source.trim();
  node.classList.toggle("is-empty", empty);
  if (empty) node.textContent = placeholder;
  else node.innerHTML = notebookMarkdown.render(source);
}
function syncMeetingWorkspace() {
  if (view !== "session" || !selected || !$("#meeting-notes")) return;
  const id = selected.id, meeting = selected.meeting || {};
  const notes = $("#meeting-notes"), enhanced = $("#enhanced-meeting-notes");
  if (!meetingDrafts.has(id) && document.activeElement !== notes) notes.value = meeting.notes || "";
  if (!enhancedMeetingDrafts.has(id) && document.activeElement !== enhanced) enhanced.value = meeting.summary || "";
  const saving = savingMeetingNotes.has(id), dirty = meetingNotesDirty();
  const enhancedSaving = savingEnhancedNotes.has(id), enhancedDirty = enhancedNotesDirty();
  $("#save-meeting-notes").disabled = saving || !dirty;
  $("#save-meeting-notes").textContent = saving ? "Saving…" : "Save notes";
  $("#meeting-notes-status").textContent = saving ? "Saving notes…" : dirty ? "Unsaved changes · draft kept on this device" : meeting.notes ? "Original notes saved on this device" : "Your notes stay yours. ⌘Enter to save.";
  const summary = state.meetingSummary || {};
  const currentBusy = generatingMeetingSummaries.has(id) || (!!summary.busy && summary.sessionId === id);
  const anyBusy = generatingMeetingSummaries.size > 0 || summary.busy;
  const importBusy = state.jobs.some((job) => ["preparing", "transcribing"].includes(job.state));
  const modelsBusy = !!state.modelInventory.busy;
  const active = state.live.active, processing = selected.status === "processing";
  const generate = $("#summarize-meeting");
  generate.disabled = !summary.ready || anyBusy || active || processing || importBusy || modelsBusy || saving || enhancedSaving || enhancedDirty || !selected.segments.length;
  generate.textContent = currentBusy ? "Enhancing…" : summary.provider ? `Enhance with ${summary.providerName}` : meeting.summary ? "Re-enhance notes" : "Enhance notes";
  const transfer = state.modelInventory.download;
  const model = summary.modelId === state.cleanupModel?.id ? state.cleanupModel : null;
  const downloading = !!summary.modelId && transfer?.id === summary.modelId;
  const usingChatGPT = summary.provider === "chatgpt";
  const setup = usingChatGPT ? "Choose a connected ChatGPT account and model in Meeting notes settings." : summary.provider ? `Save an API key for ${summary.providerName} in Meeting notes settings.` : downloading
    ? `Downloading notes model · ${formatBytes(transfer.bytes)}${model ? " of " + formatBytes(model.bytes) : ""}`
    : `Download the local notes model${model ? " · " + formatBytes(model.bytes) : ""} to get started.`;
  $("#meeting-summary-status").textContent = currentBusy ? "Filling in your notes from the conversation…" : anyBusy ? "Another meeting’s notes are being enhanced." : summary.usageLimited ? "ChatGPT plan usage limit reached. Review your limits in ChatGPT settings, then refresh your account in Meeting notes settings." : enhancedDirty ? "Save or discard enhanced edits before enhancing again." : meetingSummaryNotices.has(id) ? meetingSummaryNotices.get(id) : active ? "Keep taking notes. Enhance them when the conversation ends." : processing || importBusy ? "Wait for transcription to finish." : modelsBusy ? "Wait for the model operation to finish." : !summary.available ? "Choose a provider in Meeting notes settings." : !summary.ready ? setup : !selected.segments.length ? "The conversation transcript will provide context for your notes." : meeting.summaryStale || (meeting.summary && dirty) ? "Your notes or transcript changed. Re-enhance to include the latest details." : usingChatGPT ? `Using ChatGPT plan · ${summary.modelId}` : summary.provider ? `Uses ${summary.modelId} to process your notes and transcript text online.` : meeting.summary ? "Your original notes are always available in Your notes." : "Enhance your notes with details from the conversation.";
  $("#meeting-summary-setup").hidden = currentBusy || (!!summary.ready && !summary.provider);
  $("#manage-meeting-usage").hidden = !usingChatGPT;
  $("#manage-meeting-usage").classList.toggle("primary", !!summary.usageLimited);
  const download = $("#download-meeting-model");
  download.hidden = !!summary.provider || !summary.available || !summary.modelId;
  download.disabled = !!state.modelInventory.busy || active || anyBusy;
  download.textContent = downloading ? "Downloading…" : "Download notes model";
  $("#cancel-meeting-summary").hidden = !currentBusy || !summary.busy;
  const tab = notebookTab(id), editing = editingEnhancedNotes.has(id);
  const previewing = previewingMeetingNotes.has(id);
  $("#your-notes-panel").hidden = tab !== "notes";
  $("#enhanced-notes-panel").hidden = tab !== "enhanced";
  for (const [value, selector] of [["notes", "#your-notes-tab"], ["enhanced", "#enhanced-notes-tab"]]) {
    $(selector).setAttribute("aria-selected", String(value === tab));
    $(selector).tabIndex = value === tab ? 0 : -1;
  }
  $("#enhanced-notes-tab").textContent = enhancedDirty ? "Enhanced notes · unsaved" : enhancedNotesReady.has(id) ? "Enhanced notes · ready" : "Enhanced notes";
  const originalPreview = $("#meeting-notes-preview");
  notes.hidden = previewing;
  originalPreview.hidden = !previewing;
  $("#preview-meeting-notes").textContent = previewing ? "Edit Markdown" : "Preview Markdown";
  $("#preview-meeting-notes").disabled = !previewing && !meetingNotes().trim();
  if (tab === "notes" && previewing) showNotebookPreview(originalPreview, meetingNotes(), "Write some notes to preview them here.");
  $("#edit-enhanced-notes").hidden = editing;
  $("#edit-enhanced-notes").disabled = currentBusy || !meeting.summary;
  $("#preview-enhanced-notes").hidden = !editing;
  $("#discard-enhanced-notes").hidden = !enhancedDirty;
  $("#discard-enhanced-notes").disabled = enhancedSaving || currentBusy;
  $("#save-enhanced-notes").hidden = !editing;
  $("#save-enhanced-notes").disabled = enhancedSaving || currentBusy || !enhancedDirty;
  $("#save-enhanced-notes").textContent = enhancedSaving ? "Saving…" : "Save enhanced notes";
  enhanced.hidden = !editing;
  enhanced.disabled = currentBusy;
  const summaryNode = $("#meeting-summary");
  summaryNode.hidden = editing;
  if (tab === "enhanced" && !editing) showNotebookPreview(summaryNode, enhancedMeetingNotes(), "Your enhanced notes will appear here. Add your own notes, then choose Enhance notes.");
  $("#enhanced-notes-status").textContent = enhancedSaving ? "Saving enhanced notes…" : enhancedDirty ? "Unsaved edits · draft kept on this device" : meeting.summary ? "Enhanced notes · review the details against your conversation" : "Your original notes will remain available.";
  const canShare = tab === "enhanced" ? !!enhancedMeetingNotes() : !!meetingNotes();
  $("#copy-meeting").disabled = saving || enhancedSaving || !canShare;
  $("#copy-meeting").textContent = tab === "enhanced" ? "Copy enhanced notes" : "Copy your notes";
  $("#export-meeting").disabled = saving || enhancedSaving || !canShare;
  syncTranscriptDrawer();
}
function syncSessionTools() {
  if (!$("#copy-transcript")) return;
  syncMeetingWorkspace();
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
  const suggestion = !corrected && selected.suggestions[seg.id];
  node.classList.toggle("corrected", corrected);
  node.innerHTML = `<div class="segment-meta"><span class="source"></span>${seg.untimed ? "" : `<button class="timestamp" title="Seek media to this timestamp">${clock(seg.start)}</button>`}</div><p class="segment-text"></p><div class="segment-tools"><button class="edit-segment">Edit</button>${corrected ? '<button class="restore-segment">Restore original</button>' : ""}<span class="edited">Edited</span></div>`;
  node.querySelector(".source").textContent = seg.source;
  const text = corrected ? selected.corrections[seg.id] : seg.text;
  node.querySelector(".segment-text").textContent = text + " ";
  if (suggestion) {
    const review = document.createElement("div");
    review.className = "segment-suggestion";
    review.innerHTML = '<strong>Review suggestion</strong><span class="suggestion-text"></span><button class="accept-suggestion">Accept</button><button class="dismiss-suggestion">Dismiss</button>';
    review.querySelector(".suggestion-text").textContent = suggestion;
    const id = selected.id;
    const buttons = review.querySelectorAll("button");
    buttons.forEach((button) => {
      button.onclick = async () => {
        buttons.forEach((control) => (control.disabled = true));
        await action("resolveReviewSuggestion", { id, segment: seg.id, accept: button.classList.contains("accept-suggestion") });
        buttons.forEach((control) => (control.disabled = false));
      };
    });
    node.append(review);
  }
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
  const session = selected;
  const original = session.title;
  title.innerHTML =
    '<span class="inline-title"><input aria-label="Session title"><button>Save</button></span>';
  const input = title.querySelector("input");
  input.value = original;
  input.focus();
  const save = async () => {
    try {
      const name = input.value;
      await rpc("rename", { id: session.id, title: name });
      if (selected === session) {
        selected.title = name;
        title.textContent = name;
      }
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
const meetingPromptFields = [
  { id: "summary", label: "Empty notebook", description: "Generate meeting notes from the transcript." },
  { id: "enhancement", label: "Notebook with written notes", description: "Add transcript details to your original notes." },
];

function meetingPromptSettings() {
  if (!state.meetingPromptDefaults) return "";
  return `<details class="meeting-prompt-settings" ${meetingPromptDraft ? "open" : ""}>
    <summary>Prompts</summary>
    <p>Prompts apply to all providers. Tala keeps the structured output format. Clear a prompt to use its default.</p>
    ${meetingPromptFields.map(({ id, label, description }) => `<label class="field">
      <span>${label}</span><textarea id="meeting-prompt-${id}" rows="8" aria-describedby="meeting-prompt-${id}-help"></textarea>
    </label><div class="meeting-prompt-options"><p id="meeting-prompt-${id}-help">${description}</p>
      <button class="text-button" id="reset-meeting-prompt-${id}" aria-label="Reset ${label.toLowerCase()} prompt to default">Reset to default</button>
    </div>`).join("")}
    <div class="controls"><button id="save-meeting-prompts" class="primary">Save prompts</button></div>
  </details>`;
}

function bindMeetingPromptSettings() {
  const defaults = state.meetingPromptDefaults;
  if (!defaults) return;
  const save = $("#save-meeting-prompts");
  const saved = { ...defaults, ...state.settings.meetingPrompts };
  function sync() {
    const draft = meetingPromptDraft || saved;
    for (const { id } of meetingPromptFields) {
      const editor = $(`#meeting-prompt-${id}`);
      if (editor.value !== draft[id]) editor.value = draft[id];
      editor.readOnly = savingMeetingPrompts;
      $(`#reset-meeting-prompt-${id}`).disabled = savingMeetingPrompts || draft[id] === defaults[id];
    }
    save.disabled = savingMeetingPrompts || !meetingPromptFields.some(({ id }) => (draft[id].trim() || defaults[id]) !== saved[id]);
  }
  function edit(id, value) {
    meetingPromptDraft = { ...(meetingPromptDraft || saved), [id]: value };
    sync();
  }
  for (const { id } of meetingPromptFields) {
    const editor = $(`#meeting-prompt-${id}`);
    editor.oninput = () => edit(id, editor.value);
    $(`#reset-meeting-prompt-${id}`).onclick = () => edit(id, defaults[id]);
  }
  save.onclick = async () => {
    if (save.disabled) return;
    savingMeetingPrompts = true;
    sync();
    try {
      state.settings.meetingPrompts = await rpc("meetingPrompts", meetingPromptDraft);
      meetingPromptDraft = null;
      toast("Meeting prompts saved");
    } catch (e) {
      error(e);
    } finally {
      savingMeetingPrompts = false;
      if (view === "settings") bindMeetingPromptSettings();
    }
  };
  sync();
}

function meetingProviderSettings() {
  const providers = state.meetingProviders.providers;
  const prompts = meetingPromptSettings();
  if (!providers?.length && !prompts) return "";
  const controls = providers?.length ? `
    <label class="field"><span>Enhancement provider</span><select id="meeting-provider" aria-describedby="meeting-provider-help">
      <option value="">Local · Qwen3.5 4B Q4</option>
      ${providers.map((provider) => `<option value="${escape(provider.id)}">${escape(provider.name)}</option>`).join("")}
    </select></label>
    <p id="meeting-provider-help"></p>
    <div id="meeting-provider-fields" hidden>
      <label class="field"><span>Model ID</span><input id="meeting-provider-model" autocapitalize="off" spellcheck="false" maxlength="128"></label>
      <p id="meeting-provider-model-help"></p>
      <label class="field"><span>API key</span><input id="meeting-provider-key" type="password" autocomplete="off" spellcheck="false" maxlength="2048" aria-describedby="meeting-provider-key-help"></label>
      <p id="meeting-provider-key-help" role="status"></p>
    </div>
    <div id="meeting-chatgpt-fields" hidden>
      <label class="field"><span>ChatGPT account</span><select id="meeting-chatgpt-account"></select></label>
      <label class="field" id="meeting-chatgpt-model-field" hidden><span>ChatGPT model</span><select id="meeting-chatgpt-model"></select></label>
      <p id="meeting-chatgpt-status" role="status"></p>
      <p>OpenAI handles sign-in in your browser. Tala stores your account label and connection in macOS Keychain. When you confirm an enhancement, Tala sends your notes and transcript text to OpenAI.</p>
      <div class="controls">
        <button id="connect-chatgpt" data-oai-siwc-button>Continue with ChatGPT</button>
        <button id="cancel-chatgpt" hidden>Cancel sign-in</button>
        <button id="refresh-chatgpt-models" hidden>Refresh models</button>
        <button id="sign-out-chatgpt" hidden>Sign out</button>
        <button id="manage-chatgpt-usage" class="text-button">Manage usage</button>
        <button id="chatgpt-privacy" class="text-button">OpenAI privacy policy</button>
      </div>
    </div>
    <div class="controls"><button id="save-meeting-provider" class="primary">Save provider</button><button id="remove-meeting-provider-key" hidden>Remove saved key</button></div>` : "";
  return `<section class="settings-group" aria-labelledby="meeting-provider-heading">
    <h2 id="meeting-provider-heading">Meeting notes</h2>${controls}${prompts}
  </section>`;
}

function renderChatGPTAccounts(id, model) {
  const accounts = state.meetingProviders.chatGPTAccounts || [];
  const account = accounts.find((entry) => entry.id === id);
  const select = $("#meeting-chatgpt-account"), models = $("#meeting-chatgpt-model");
  select.innerHTML = '<option value="">Connect an account…</option>' + accounts.map((entry) =>
    `<option value="${escape(entry.id)}">${escape(entry.label)}</option>`).join("");
  select.value = account?.id || "";
  models.innerHTML = (account?.models || []).map((entry) =>
    `<option value="${escape(entry.id)}">${escape(entry.name)}</option>`).join("");
  models.value = account?.models?.some((entry) => entry.id === model) ? model : account?.models?.[0]?.id || "";
}

function updateMeetingProviderControls() {
  if (view !== "settings" || !state.meetingProviders.providers?.length) return;
  const select = $("#meeting-provider");
  if (!select) return;
  const provider = state.meetingProviders.providers.find((entry) => entry.id === select.value);
  const busy = savingMeetingProvider || changingChatGPTAccount || state.meetingSummary.busy;
  const chatGPT = provider?.id === "chatgpt";
  const accountSelect = $("#meeting-chatgpt-account"), chatGPTModel = $("#meeting-chatgpt-model");
  const account = state.meetingProviders.chatGPTAccounts?.find((entry) => entry.id === accountSelect.value);
  const key = $("#meeting-provider-key"), model = $("#meeting-provider-model");
  select.disabled = model.disabled = key.disabled = accountSelect.disabled = chatGPTModel.disabled = !!busy;
  $("#meeting-provider-fields").hidden = !provider || chatGPT;
  $("#meeting-chatgpt-fields").hidden = !chatGPT;
  $("#meeting-provider-help").textContent = !provider
    ? "Runs on your Mac after you download Qwen."
    : chatGPT ? "Use an eligible ChatGPT plan or credits for note enhancement. Recording and transcription run on your Mac."
    : "Tala asks before sending meeting notes and transcript text to this provider. Recording and transcription run on your Mac." +
      (provider.id === "gemini" ? " Google’s unpaid tier may use your text to improve its products." : "");
  $("#meeting-provider-model-help").textContent = provider ? `Leave blank to use ${provider.defaultModel}. Your API account must have access to the model. Requests count toward your quota and may incur charges.` : "";
  key.placeholder = provider?.hasKey ? "Enter a new key to replace it" : "Paste your provider API key";
  model.placeholder = provider?.defaultModel || "";
  $("#meeting-provider-key-help").textContent = (provider?.hasKey ? "Key saved in macOS Keychain." : "Tala stores your key in macOS Keychain.") +
    (provider?.id === "anthropic" ? " Claude enhancement uses a Claude Console API key and separate API billing." : "");
  $("#save-meeting-provider").disabled = !!busy || (chatGPT ? !account?.authorized || !chatGPTModel.value : !!provider && !provider.hasKey && !key.value.trim());
  $("#remove-meeting-provider-key").hidden = !provider?.hasKey;
  $("#remove-meeting-provider-key").disabled = !!busy;
  $("#connect-chatgpt").disabled = !!busy;
  $("#cancel-chatgpt").hidden = changingChatGPTAccount !== "signIn";
  $("#sign-out-chatgpt").hidden = !account?.signedIn;
  $("#sign-out-chatgpt").disabled = !!busy;
  $("#refresh-chatgpt-models").hidden = !account?.authorized;
  $("#refresh-chatgpt-models").disabled = !!busy;
  $("#meeting-chatgpt-model-field").hidden = !account?.authorized || !account.models?.length;
  $("#meeting-chatgpt-status").textContent = changingChatGPTAccount === "signIn" ? "Finish signing in in your browser." : account?.authorized
    ? "ChatGPT plan usage is enabled for this account." : account?.signedIn
      ? "Allow ChatGPT plan usage to enhance notes with this account." : "Connect a ChatGPT account to load its available models.";
}

function bindMeetingProviderSettings() {
  if (!state.meetingProviders.providers?.length) return;
  const select = $("#meeting-provider"), model = $("#meeting-provider-model"), key = $("#meeting-provider-key");
  select.value = state.meetingProviders.provider || "";
  model.value = state.meetingProviders.model || "";
  renderChatGPTAccounts(state.meetingProviders.account, state.meetingProviders.model);
  select.onchange = () => {
    model.value = select.value === state.meetingProviders.provider ? state.meetingProviders.model : "";
    key.value = "";
    updateMeetingProviderControls();
  };
  key.oninput = updateMeetingProviderControls;
  const account = $("#meeting-chatgpt-account"), chatGPTModel = $("#meeting-chatgpt-model");
  account.onchange = () => {
    const saved = state.meetingProviders;
    renderChatGPTAccounts(account.value, account.value === saved.account ? saved.model : "");
    updateMeetingProviderControls();
  };
  chatGPTModel.onchange = updateMeetingProviderControls;
  async function changeAccount(operation) {
    if (savingMeetingProvider || changingChatGPTAccount || state.meetingSummary.busy) return;
    const selectedModel = chatGPTModel.value;
    changingChatGPTAccount = operation;
    updateMeetingProviderControls();
    try {
      const result = await rpc("chatGPTAccount", { operation, account: account.value });
      state.meetingProviders = result;
      if (view === "settings") renderChatGPTAccounts(result.selected, selectedModel);
      if (result.welcome) window.alert("Your ChatGPT plan is connected. Save this provider to use your plan or credits for eligible note enhancements. Manage usage in ChatGPT settings.");
      if (result.notice) toast(result.notice);
      else if (operation === "signOut") toast("Signed out of ChatGPT in Tala");
      else if (operation === "signIn") toast("ChatGPT account connected");
    } catch (e) {
      if (/context cancel+ed/i.test(e.message)) toast("ChatGPT sign-in canceled");
      else error(e);
    } finally {
      changingChatGPTAccount = "";
      updateMeetingProviderControls();
    }
  }
  $("#connect-chatgpt").onclick = () => changeAccount("signIn");
  $("#cancel-chatgpt").onclick = () => action("cancelChatGPTConnection");
  $("#refresh-chatgpt-models").onclick = () => changeAccount("refresh");
  $("#sign-out-chatgpt").onclick = () => changeAccount("signOut");
  $("#manage-chatgpt-usage").onclick = () => action("manageChatGPTUsage");
  $("#chatgpt-privacy").onclick = () => action("chatGPTPrivacy");
  async function save(operation, data, message) {
    if (savingMeetingProvider || changingChatGPTAccount || state.meetingSummary.busy) return;
    savingMeetingProvider = true;
    updateMeetingProviderControls();
    try {
      state.meetingProviders = await rpc(operation, data);
      if (view === "settings") {
        $("#meeting-provider-key").value = "";
        if (operation === "meetingProvider") {
          $("#meeting-provider").value = state.meetingProviders.provider;
          $("#meeting-provider-model").value = state.meetingProviders.model;
          renderChatGPTAccounts(state.meetingProviders.account, state.meetingProviders.model);
        }
      }
      toast(message);
    } catch (e) {
      error(e);
    } finally {
      savingMeetingProvider = false;
      updateMeetingProviderControls();
    }
  }
  $("#save-meeting-provider").onclick = () => {
    const chatGPT = select.value === "chatgpt";
    return save("meetingProvider", { provider: select.value, model: chatGPT ? chatGPTModel.value : model.value, key: chatGPT ? "" : key.value, account: chatGPT ? account.value : "" }, "Meeting notes provider saved");
  };
  $("#remove-meeting-provider-key").onclick = () => {
    const provider = state.meetingProviders.providers.find((entry) => entry.id === select.value);
    if (provider?.hasKey && window.confirm(`Remove the saved ${provider.name} API key from Tala?`)) {
      return save("removeMeetingProviderKey", provider.id, "Saved API key removed");
    }
  };
  updateMeetingProviderControls();
}

function liveReviewSettings() {
  return `<section class="settings-group" aria-labelledby="live-review-heading">
    <h2 id="live-review-heading">Live transcript review</h2>
    <label><input id="live-review-enabled" type="checkbox" ${state.settings.liveReview ? "checked" : ""}> Suggest corrections while recording</label>
    <p>Off by default to keep recording responsive. Turn it on to get optional suggestions. You decide which ones to accept.</p>
    <label class="field"><span>Review model</span><select id="live-review-model" aria-describedby="live-review-help live-review-download-status">
      <option value="">Reuse recording model</option>
      ${state.modelCatalog.map((model) => `<option value="${escape(model.id)}">${escape(model.name)} · audio review</option>`).join("")}
      ${state.cleanupModel ? `<option value="${escape(state.cleanupModel.id)}">${escape(state.cleanupModel.name)} · text cleanup</option>` : ""}
    </select></label>
    <p id="live-review-help"></p>
    <p id="live-review-download-status" role="status"></p>
    <div class="controls"><button id="download-live-review">Download model</button><button id="remove-live-review" hidden>Remove model</button><button id="save-live-review">Save live review</button></div>
  </section>`;
}
function updateLiveReviewControls() {
  if (view !== "settings") return;
  const select = $("#live-review-model");
  if (!select) return;
  const reuse = select.value === "";
  const cleanup = state.cleanupModel?.id === select.value;
  const model = cleanup ? state.cleanupModel : state.modelCatalog.find((entry) => entry.id === select.value);
  const status = modelStatus(select.value), installed = !!status.path;
  const transfer = state.modelInventory.download;
  const download = $("#download-live-review");
  download.hidden = reuse || installed;
  download.disabled = !model || state.modelInventory.busy || state.live.active;
  const remove = $("#remove-live-review");
  remove.hidden = !cleanup || !status.managed;
  remove.disabled = state.modelInventory.busy || state.live.active;
  $("#save-live-review").disabled = state.live.active ||
    (!reuse && (!model || ($("#live-review-enabled").checked && !installed)));
  $("#live-review-help").textContent = reuse
    ? "Rechecks uncertain words against the audio when the recording model is idle. Uses the model you choose for each recording. No extra download."
    : cleanup
      ? `${model.description} Running it during recording uses extra memory and processing. The same download powers Enhance notes after recording.`
      : "Rechecks uncertain words against the audio. If the recording uses this model, checks run when it is idle. Using different models loads both into memory during recording.";
  $("#live-review-download-status").textContent = !model ? "" : installed ? `Ready for offline review · ${formatBytes(model.bytes)}` :
    transfer?.id === model.id ? `${formatBytes(transfer.bytes)} of ${formatBytes(model.bytes)} downloaded` :
    `Free download · ${formatBytes(model.bytes)}`;
}
async function saveLiveReview() {
  const button = $("#save-live-review");
  const enabled = $("#live-review-enabled").checked;
  const modelId = $("#live-review-model").value;
  button.disabled = true;
  try {
    await rpc("liveReview", { enabled, modelId });
    state.settings.liveReview = enabled;
    state.settings.liveReviewModel = modelId;
    if (view === "settings") renderSettings();
    toast("Live review settings saved");
  } catch (e) {
    error(e);
  } finally {
    updateLiveReviewControls();
  }
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
function renderModelLibrary() {
  const library = view === "settings" && $("#model-library");
  if (!library) return;
  const inventory = state.modelInventory, installed = installedModels();
  const disabled = inventory.busy ? "disabled" : "";
  const chosenDefault = preferredModelId();
  library.innerHTML = `<ul class="model-library">${state.modelCatalog.map((m) => {
    const status = modelStatus(m.id), activeDownload = inventory.download?.id === m.id;
    const note = status.path ? (status.managed ? " · Installed" : " · Available from disk") : "";
    const tag = m.id === chosenDefault ? "Default" : m.id === state.defaultModelId ? "Recommended" : "";
    return `<li class="model-entry">
      <div><strong>${escape(m.use)}</strong>${tag ? `<span class="model-tag">${tag}</span>` : ""}
        <small>${escape(m.name)} · ${formatBytes(m.bytes)}${note}</small>
        ${m.description ? `<p>${escape(m.description)}</p>` : ""}
        ${activeDownload ? '<progress id="model-download-progress" aria-label="Model download progress"></progress><small id="model-download-status" role="status"></small>' : ""}
        ${status.state === "missing" && status.detail?.includes("SHA-256") ? `<small class="field-error">${escape(status.detail)}</small>` : ""}
      </div>
      ${status.path
        ? (status.managed ? `<button data-model-remove="${escape(m.id)}" aria-label="Remove ${escape(m.name)}" ${disabled}>Remove</button>` : "")
        : `<button data-model-download="${escape(m.id)}" aria-label="Download ${escape(m.name)}" class="${m.id === chosenDefault ? "primary" : ""}" ${disabled}>${activeDownload ? "Downloading…" : "Download"}</button>`}
    </li>`;
  }).join("")}</ul>
  <div class="model-preferences">
    <label class="field"><span>Default model</span><select id="model-default" ${!installed.length || savingModelPreference ? "disabled" : ""}>
      ${installed.some((m) => m.id === chosenDefault) ? "" : '<option value="" selected disabled>Choose an installed model</option>'}
      ${installed.map((m) => `<option value="${escape(m.id)}" ${m.id === chosenDefault ? "selected" : ""}>${escape(m.use)}</option>`).join("")}</select></label>
    <label class="field"><span>Before transcription</span><select id="model-behavior" ${savingModelPreference ? "disabled" : ""}>
      <option value="default" ${state.settings.askModel ? "" : "selected"}>Use my default</option>
      <option value="ask" ${state.settings.askModel ? "selected" : ""}>Ask each time</option></select></label>
  </div>
  <p>Choose a different installed model before you start a recording or queue an import.</p>`;
  library.querySelectorAll("[data-model-download]").forEach((button) => {
    button.onclick = () => changeInstalledModel(button, "downloadModel", button.dataset.modelDownload);
  });
  library.querySelectorAll("[data-model-remove]").forEach((button) => {
    button.onclick = () => {
      const id = button.dataset.modelRemove, model = state.modelCatalog.find((m) => m.id === id);
      if (window.confirm(`Remove ${model.name}? You can download it again from Settings.`))
        changeInstalledModel(button, "removeModel", id);
    };
  });
  $("#model-default").onchange = (event) => saveModelPreference(event.target.value, !!state.settings.askModel);
  $("#model-behavior").onchange = (event) => saveModelPreference("", event.target.value === "ask");
  updateModelTransfer();
}
function updateModelTransfer() {
  if (view !== "settings") return;
  const progress = $("#model-download-progress"), label = $("#model-download-status");
  const transfer = state.modelInventory.download;
  const model = state.modelCatalog.find((m) => m.id === transfer?.id);
  if (!progress || !label || !model) return;
  progress.max = model.bytes;
  progress.value = transfer.bytes;
  label.textContent = transfer.bytes >= model.bytes ? "Verifying download…" :
    `${formatBytes(transfer.bytes)} of ${formatBytes(model.bytes)}`;
}
async function changeInstalledModel(button, operation, id) {
  button.disabled = true;
  try {
    await rpc(operation, id);
    toast(operation === "downloadModel" ? "Model ready" : "Model removed");
  } catch (e) {
    error(e);
  } finally {
    button.disabled = false;
  }
}
async function saveModelPreference(id, ask) {
  if (savingModelPreference) return;
  savingModelPreference = true;
  renderModelLibrary();
  try {
    await rpc("modelPreference", { id, ask });
  } catch (e) {
    error(e);
  } finally {
    savingModelPreference = false;
    renderModelLibrary();
  }
}
function renderSettings() {
  const s = state.settings;
  page(
    `<h1 class="visually-hidden">Settings</h1>
    <section class="settings-group" aria-labelledby="models-heading">
      <h2 id="models-heading">Transcription models</h2>
      <p>Start with the default for live recording and everyday imports. Add another model for difficult audio or a machine with less memory. Installed models work offline.</p>
      <div id="model-library"></div>
      <small id="tensor-acceleration" class="muted" role="status">${escape(state.tensorAcceleration || "Tensor acceleration status appears after Whisper loads.")}</small>
      <details class="model-file-settings">
        <summary>Use an existing model file</summary>
        <label class="field"><span>Existing model path</span><input id="model-path" value="${escape(s.modelPath || "")}" placeholder="/path/to/ggml-model.bin"></label>
        <label class="field"><span>Additional model directory</span><input id="model-dir" value="${escape(s.modelDir || "")}" placeholder="Optional directory to search"></label>
        <div class="controls"><button id="choose-model">Choose model file…</button><button id="verify-model">Save and verify model</button></div>
      </details>
    </section>
    ${meetingProviderSettings()}
    ${appearanceSettings()}<div class="settings-group"><h2>Recognition</h2><label class="field"><span>Language</span><select id="language"><option value="taglish">Taglish (English and Tagalog)</option><option value="auto">Automatic (all supported languages)</option><option value="en">English</option><option value="tl">Filipino / Tagalog</option></select></label><label class="field"><span>Vocabulary hints (optional)</span><textarea id="context-prompt" aria-describedby="vocabulary-help" placeholder="e.g. Tala, Ateneo de Manila, Kubernetes, barangay"></textarea></label><p id="vocabulary-help">Help Whisper recognize unusual names, acronyms, and technical terms by listing words you expect to hear, or adding a short description of the recording. For example: “A Taglish lecture about Kubernetes at Ateneo de Manila.” This is a recognition hint, not a replacement list or an instruction to rewrite text. It may improve spelling but does not guarantee it. Leave blank for general recordings. Applies to new audio in live, file, and URL transcriptions.</p><p>Taglish skips chunks that Whisper detects as languages other than English or Tagalog. Choose Automatic to include other languages. Translation is disabled.</p><label><input id="skip-quiet-imports" type="checkbox" aria-describedby="skip-quiet-help" ${s.skipQuietImports ? "checked" : ""}> Skip nearly silent import windows</label><p id="skip-quiet-help">Speeds up file and URL imports with long pauses by skipping windows that stay nearly silent throughout. Source timestamps are preserved. Leave off for very faint recordings.</p><button id="save-settings" class="primary">Save settings</button></div>${liveReviewSettings()}<div class="settings-group"><h2>Application</h2><label><input id="menu-bar-icon" type="checkbox" ${s.menuBarIcon ? "checked" : ""}> Show Tala in the menu bar</label><p>Off by default. Enable quick recording controls in the menu bar. You can always reopen Tala from the Dock.</p></div><div class="settings-group"><h2>Media tools</h2><p>Tala uses FFmpeg for media files and yt-dlp for supported links.</p><div id="dependency-checks" aria-live="polite"></div>${signInHelper()}</div><div class="settings-group"><h2>Storage</h2><p>${escape(state.output || "~/Documents/Tala/")}<br>Temporary audio is deleted after processing.</p><button id="open-storage">Open transcript folder</button></div>`,
  );
  renderModelLibrary();
  bindMeetingProviderSettings();
  bindMeetingPromptSettings();
  $("#save-live-review").onclick = saveLiveReview;
  const savedReview = s.liveReviewModel || "";
  $("#live-review-model").value = savedReview === state.cleanupModel?.id || state.modelCatalog.some((model) => model.id === savedReview)
    ? savedReview : "";
  $("#live-review-model").onchange = updateLiveReviewControls;
  $("#live-review-enabled").onchange = updateLiveReviewControls;
  $("#download-live-review").onclick = async () => {
    await changeInstalledModel($("#download-live-review"), "downloadModel", $("#live-review-model").value);
    updateLiveReviewControls();
  };
  $("#remove-live-review").onclick = async () => {
    if (!window.confirm("Remove the downloaded Qwen cleanup model?")) return;
    await changeInstalledModel($("#remove-live-review"), "removeModel", state.cleanupModel.id);
    updateLiveReviewControls();
  };
  updateLiveReviewControls();
  bindAppearanceSettings();
  bindSignInHelper();
  renderDependencyChecks();
  refreshDependencies();
  $("#language").value = s.language || "taglish";
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
      state.settings = (await rpc("settings", settings)) || settings;
      renderSettings();
      toast("Model verified");
    } catch (e) {
      error(e);
    } finally {
      if ($("#verify-model")) $("#verify-model").disabled = false;
    }
  };
  $("#save-settings").onclick = async () => {
    const b = $("#save-settings");
    b.disabled = true;
    try {
      const settings = readSettings();
      state.settings = (await rpc("settings", settings)) || settings;
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
  if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "f") {
    e.preventDefault();
    if (view === "session") {
      setTranscriptVisible(true);
      $("#transcript-search").focus();
    }
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
