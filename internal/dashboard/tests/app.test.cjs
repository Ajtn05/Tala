const { test } = require("node:test");
const assert = require("node:assert/strict");
const { readFileSync } = require("node:fs");
const { join } = require("node:path");
const vm = require("node:vm");
const markdown = readFileSync(join(__dirname, "../assets/markdown-it.umd.min.js"), "utf8");

// Exercise the production event handlers without a browser-specific File.path.
// Native pasteboard resolution is mocked at the bridge boundary.
function harness({ deferActions = [], snapshot: overrides = {}, results = {}, preferences = {}, confirm = () => true, alert = () => {} } = {}) {
  const nodes = new Map(), listeners = new Map(), requests = [];
  function element(selector) {
    if (!nodes.has(selector)) nodes.set(selector, {
      innerHTML: "", textContent: "", hidden: false, value: "",
      style: {}, dataset: {}, classList: { add() {}, remove() {}, toggle() {} },
      setAttribute() {}, focus() {}, querySelectorAll() { return []; },
    });
    return nodes.get(selector);
  }
  const ready = { ready: true, missing: [] };
  const snapshot = { sessions: [], jobs: [], live: { active: false, mode: 0 },
    model: { state: "reused", path: "/model.bin" }, settings: { language: "auto" },
    dependencies: { tools: {}, file: ready, url: ready }, ...overrides };
  const window = { talaPreferences: preferences, addEventListener() {}, getSelection() { return ""; }, confirm, alert };
  const document = { querySelector: element, querySelectorAll() { return []; },
    body: element("body"), documentElement: element("html"), addEventListener(name, callback) { listeners.set(name, callback); } };
  window.webkit = { messageHandlers: { tala: { postMessage(request) {
    requests.push(request);
    if (deferActions.includes(request.action)) return;
    if (request.action === "clearQueue") snapshot.jobs = [];
    const result = Object.hasOwn(results, request.action) ? (typeof results[request.action] === "function" ? results[request.action](request.data) : results[request.action]) : request.action === "snapshot" ? snapshot
      : request.action === "dependencies" ? snapshot.dependencies
      : request.action === "probe" ? { duration: 11, format: "mp3" }
      : request.action === "metadata" ? { url: request.data, title: "Lecture", duration: 125 } : null;
    queueMicrotask(() => window.receive({ type: "reply", data: { id: request.id, result } }));
  } } } };
  const context = vm.createContext({ URL, window, document, setInterval() {}, setTimeout() {}, clearTimeout() {} });
  vm.runInContext(markdown, context);
  vm.runInContext(readFileSync(join(__dirname, "../assets/app.js"), "utf8"), context);
  return { context, window, listeners, requests, element };
}
const flush = () => new Promise(setImmediate);

test("live review downloads an available model and saves an opt-in preference", async () => {
  const model = { id: "qwen3.5-4b", name: "Qwen3.5 4B Q4", bytes: 2783446368, description: "Uses transcript text to suggest wording and punctuation." };
  const whisper = { id: "large-v3-turbo-q5", name: "Whisper Large v3 Turbo Q5", bytes: 574041195 };
  const h = harness({ snapshot: { modelCatalog: [whisper], cleanupModel: model, modelInventory: { statuses: {} } } });
  await flush();
  vm.runInContext('navigate("settings")', h.context);
  assert.doesNotMatch(h.element("#content").innerHTML, /id="live-review-enabled"[^>]*checked/);
  assert.equal(h.element("#live-review-model").value, "");
  assert.match(h.element("#live-review-help").textContent, /No extra download/);
  assert.equal(h.element("#download-live-review").hidden, true);
  h.element("#live-review-enabled").checked = true;
  h.element("#live-review-enabled").onchange();
  assert.equal(h.element("#save-live-review").disabled, false);
  assert.match(h.element("#content").innerHTML, /Qwen3\.5 4B Q4 · text cleanup/);
  assert.doesNotMatch(h.element("#model-library").innerHTML, /Qwen/);
  h.element("#live-review-model").value = model.id;
  h.element("#live-review-model").onchange();
  assert.match(h.element("#live-review-help").textContent, /same download powers Enhance notes/);
  assert.match(h.element("#live-review-download-status").textContent, /Free download/);
  h.element("#live-review-enabled").checked = true;
  h.element("#live-review-enabled").onchange();
  assert.equal(h.element("#save-live-review").disabled, true);
  await h.element("#download-live-review").onclick();
  assert.equal(h.requests.findLast((request) => request.action === "downloadModel").data, model.id);
  h.window.receive({ type: "modelInventory", data: { statuses: { [model.id]: { path: "/model.bin" } } } });
  assert.equal(h.element("#save-live-review").disabled, false);
  assert.equal(h.element("#download-live-review").hidden, true);
  await h.element("#save-live-review").onclick();
  const save = h.requests.findLast((request) => request.action === "liveReview");
  assert.equal(save.data.enabled, true);
  assert.equal(save.data.modelId, model.id);
  assert.match(h.element("#content").innerHTML, /id="live-review-enabled"[^>]*checked/);
});

test("review suggestions remain separate from the transcript until accepted", async () => {
  const h = harness();
  await flush();
  vm.runInContext('selected = { id: "recording", segments: [], suggestions: {}, corrections: {} }', h.context);
  const data = { sessionId: "recording", segmentId: "line", text: "I can't send it Friday." };
  h.window.receive({ type: "reviewSuggestion", data });
  assert.equal(vm.runInContext("selected.suggestions.line", h.context), data.text);
  assert.equal(vm.runInContext("selected.corrections.line", h.context), undefined);
  h.window.receive({ type: "reviewSuggestion", data: { ...data, text: null } });
  assert.equal(vm.runInContext("selected.suggestions.line", h.context), undefined);
  h.window.receive({ type: "reviewSuggestion", data });
  h.window.receive({ type: "reviewSuggestion", data: { ...data, accepted: true } });
  assert.equal(vm.runInContext("selected.suggestions.line", h.context), undefined);
  assert.equal(vm.runInContext("selected.corrections.line", h.context), data.text);
});

test("the quiet import toggle is shown only in settings and saved there", async () => {
  const h = harness();
  await flush();
  assert.doesNotMatch(h.element("#content").innerHTML, /Skip nearly silent import windows/);
  vm.runInContext('navigate("settings")', h.context);
  assert.match(h.element("#content").innerHTML, /id="skip-quiet-imports"[^>]*>/);
  assert.doesNotMatch(h.element("#content").innerHTML, /id="skip-quiet-imports"[^>]*checked/);
  h.element("#skip-quiet-imports").checked = true;
  await h.element("#save-settings").onclick();
  const save = h.requests.findLast((request) => request.action === "settings");
  assert.equal(save.data.skipQuietImports, true);
  assert.match(h.element("#content").innerHTML, /id="skip-quiet-imports"[^>]*checked/);
});

test("the tensor indicator updates without rebuilding open settings", async () => {
  const h = harness();
  await flush();
  vm.runInContext('view = "settings"; renderSettings()', h.context);
  assert.match(h.element("#content").innerHTML, /Tensor acceleration status appears after Whisper loads/);

  const draft = h.element("#context-prompt");
  draft.value = "unsaved vocabulary";
  const settings = h.element("#content").innerHTML;
  h.window.receive({ type: "tensorAcceleration", data: "Tensor acceleration active" });
  assert.equal(h.element("#tensor-acceleration").textContent, "Tensor acceleration active");
  assert.equal(h.element("#content").innerHTML, settings);
  assert.equal(draft.value, "unsaved vocabulary");

  vm.runInContext('applySnapshot({ tensorAcceleration: "Tensor acceleration inactive" })', h.context);
  assert.match(h.element("#content").innerHTML, /Tensor acceleration inactive/);
});

test("file drags are accepted and resolve paths through the native bridge", async () => {
  const h = harness();
  await flush();
  const event = { dataTransfer: { types: ["Files"], files: [{ name: "lecture.mp3" }] },
    prevented: 0, preventDefault() { this.prevented++; } };
  h.listeners.get("dragover")(event);
  assert.equal(event.dataTransfer.dropEffect, "copy");
  h.listeners.get("drop")(event);
  await flush();
  assert.equal(event.prevented, 2);
  assert.equal(h.requests.at(-1).action, "droppedFiles");
  assert.equal(vm.runInContext("pending.size", h.context), 0);
  // The native reply carries absolute paths; probing and review use those paths.
  h.window.receive({ type: "files", data: ["/recordings/lecture.mp3"] });
  await flush();
  assert.ok(h.requests.some(r => r.action === "probe" && r.data === "/recordings/lecture.mp3"));
  assert.equal(vm.runInContext("view", h.context), "new");
  assert.match(h.element("#files").innerHTML, /lecture.mp3/);
  assert.match(h.element("#files").innerHTML, /00:11/);
});

test("repeated drops deduplicate paths and preserve earlier selected files", async () => {
  const h = harness();
  await flush();
  h.window.receive({ type: "files", data: ["/first.wav", "/first.wav"] });
  await flush();
  h.window.receive({ type: "files", data: ["/first.wav", "/second.wav"] });
  await flush();
  assert.equal(vm.runInContext("files.length", h.context), 2);
  assert.equal(h.requests.filter(r => r.action === "probe").length, 2);
  h.window.receive({ type: "files", data: [] });
  assert.equal(vm.runInContext("files.length", h.context), 2);
});

test("text drags do not read a stale file pasteboard", async () => {
  const h = harness();
  await flush();
  const count = h.requests.length;
  h.listeners.get("drop")({ dataTransfer: { types: ["text/plain"] },
    preventDefault() { assert.fail("ordinary text drag was intercepted"); } });
  assert.equal(h.requests.length, count);
});

function typeURL(h, value) {
  const input = h.element("#media-url");
  input.value = value;
  input.oninput({ target: input });
}

test("home recording uses the selected audio source and the native start action", async () => {
  const h = harness();
  await flush();
  const source = h.element("#audio-source");
  source.value = "2";
  await source.onchange({ target: source });
  await h.element("#start-recording").onclick();
  const actions = h.requests.filter(r => r.action === "mode" || r.action === "start");
  assert.deepEqual(actions.map(r => [r.action, r.data]), [["mode", 2], ["start", null]]);
  assert.equal(vm.runInContext("state.live.mode", h.context), 2);
});

test("home updates preserve a media draft and do not rebuild the input panel", async () => {
  const h = harness();
  await flush();
  typeURL(h, "https://example.org/lecture");
  const page = h.element("#content").innerHTML;
  h.window.receive({ type: "jobs", data: [{ id: "job", title: "Lecture", state: "transcribing", progress: 0.5 }] });
  h.window.receive({ type: "sessions" });
  h.window.receive({ type: "model", data: { state: "reused", path: "/model.bin" } });
  await flush();
  assert.equal(h.element("#content").innerHTML, page);
  assert.equal(h.element("#media-url").value, "https://example.org/lecture");
  assert.equal(vm.runInContext("urlDraft", h.context), "https://example.org/lecture");
  assert.match(h.element("#home-queue").innerHTML, /50%/);
});

test("editing a link during metadata lookup discards the stale result", async () => {
  const h = harness({ deferActions: ["metadata"] });
  await flush();
  typeURL(h, "https://example.org/first");
  const lookup = h.element("#fetch-url").onclick();
  const request = h.requests.at(-1);
  assert.equal(request.action, "metadata");
  typeURL(h, "https://example.org/second");
  h.window.receive({ type: "reply", data: { id: request.id, result: {
    url: "https://example.org/first", title: "First", duration: 60,
  } } });
  await lookup;
  assert.equal(vm.runInContext("urlMeta", h.context), null);
  assert.equal(h.element("#url-details").innerHTML, "");
  assert.equal(h.element("#fetch-url").disabled, false);
});

test("invalid media links stay in the form and never reach the native lookup", async () => {
  const h = harness();
  await flush();
  typeURL(h, "not a link");
  await h.element("#fetch-url").onclick();
  assert.equal(h.requests.filter(r => r.action === "metadata").length, 0);
  assert.equal(h.element("#url-error").hidden, false);
});

test("reviewed files enqueue from home and open the processing queue", async () => {
  const h = harness();
  await flush();
  h.window.receive({ type: "files", data: ["/recordings/lecture.mp3"] });
  await flush();
  await h.element("#enqueue-files").onclick();
  const request = h.requests.find(r => r.action === "enqueue");
  assert.equal(request.data.kind, "file");
  assert.deepEqual(Array.from(request.data.sources), ["/recordings/lecture.mp3"]);
  assert.equal(vm.runInContext("files.length", h.context), 0);
  assert.equal(vm.runInContext("view", h.context), "queue");
});

test("session sorting and grouping preserve sessions and order within each group", async () => {
  const h = harness();
  await flush();
  const result = vm.runInContext(`(() => {
    const items = [
      { id: 'a', title: 'Meeting 10', created: '2026-10-02', type: 'file', duration: 30 },
      { id: 'b', title: 'Meeting 2', created: '2026-11-01', type: 'live', duration: 90 },
      { id: 'c', title: 'Meeting 3', created: '2026-11-05', type: 'file', duration: 60 },
    ];
    return JSON.stringify({
      titles: organizeSessions(items, 'title', 'none')[0].sessions.map(s => s.id),
      lengths: organizeSessions(items, 'longest', 'none')[0].sessions.map(s => s.id),
      months: organizeSessions(items, 'oldest', 'month').map(g => g.sessions.map(s => s.id)),
      sources: organizeSessions(items, 'newest', 'type').map(g => [g.title, g.sessions.map(s => s.id)]),
      original: items.map(s => s.id),
    });
  })()`, h.context);
  assert.deepEqual(JSON.parse(result), {
    titles: ['b', 'c', 'a'], lengths: ['b', 'c', 'a'],
    months: [['a'], ['b', 'c']],
    sources: [['Live recording', ['b']], ['Media file', ['c', 'a']]],
    original: ['a', 'b', 'c'],
  });
});

test("day grouping uses local calendar dates and preserves the selected session order", async () => {
  const h = harness();
  await flush();
  const result = vm.runInContext(`(() => {
    const items = [
      { id: 'late', title: 'Late meeting', created: new Date(2026, 10, 7, 23, 59).toISOString() },
      { id: 'early', title: 'Zulu meeting', created: new Date(2026, 10, 8, 0, 1).toISOString() },
      { id: 'afternoon', title: 'Alpha meeting', created: new Date(2026, 10, 8, 16).toISOString() },
    ];
    const grouped = sort => organizeSessions(items, sort, 'day').map(g => [g.key, g.sessions.map(s => s.id)]);
    return JSON.stringify({ newest: grouped('newest'), oldest: grouped('oldest'), title: grouped('title') });
  })()`, h.context);
  assert.deepEqual(JSON.parse(result), {
    newest: [['2026-11-08', ['afternoon', 'early']], ['2026-11-07', ['late']]],
    oldest: [['2026-11-07', ['late']], ['2026-11-08', ['early', 'afternoon']]],
    title: [['2026-11-08', ['afternoon', 'early']], ['2026-11-07', ['late']]],
  });
});

test("sort, group, and appearance choices are persisted through native preferences", async () => {
  const h = harness();
  await flush();
  vm.runInContext('navigate("history")', h.context);
  h.element("#history-sort").onchange({ target: { value: "title" } });
  h.element("#history-group").onchange({ target: { value: "day" } });
  vm.runInContext('navigate("settings")', h.context);
  h.element("#appearance").onchange({ target: { value: "dark" } });
  vm.runInContext('storage.setItem("colorScheme", "iris"); applyAppearance()', h.context);
  assert.equal(h.element("html").dataset.appearance, "dark");
  assert.equal(h.element("html").dataset.scheme, "iris");
  assert.deepEqual(h.requests.filter(r => r.action === "preference").map(r => [r.data.key, r.data.value]), [
    ["historySort", "title"], ["historyGroup", "day"], ["appearance", "dark"], ["colorScheme", "iris"],
  ]);
});

test("clearing an active queue requires confirmation and uses the native clear action", async () => {
  const h = harness({ snapshot: { jobs: [{ id: "job", title: "Lecture", state: "waiting" }] } });
  await flush();
  vm.runInContext('navigate("queue")', h.context);
  h.window.confirm = () => false;
  await h.element("#clear-queue").onclick();
  assert.equal(h.requests.some(r => r.action === "clearQueue"), false);
  h.window.confirm = () => true;
  await h.element("#clear-queue").onclick();
  assert.equal(h.requests.filter(r => r.action === "clearQueue").length, 1);
  assert.equal(vm.runInContext('state.jobs.length', h.context), 0);
});

test("file ranges preserve drafts and independently select source intervals", async () => {
  const h = harness();
  await flush();
  h.window.receive({ type: "files", data: ["/first.wav", "/second.wav"] });
  await flush();
  h.element("#file-range-0-start").oninput({ target: { value: "00:02.500" } });
  h.element("#file-range-0-end").oninput({ target: { value: "9" } });
  h.element("#file-range-1-end").oninput({ target: { value: "5" } });
  vm.runInContext('navigate("settings"); navigate("new")', h.context);
  assert.match(h.element("#files").innerHTML, /value="00:02.500"/);
  await h.element("#enqueue-files").onclick();
  const jobs = h.requests.filter(r => r.action === "enqueue").map(r => JSON.parse(JSON.stringify(r.data)));
  assert.deepEqual(jobs, [
    { sources: ["/first.wav"], kind: "file", range: { start: 2.5, end: 9 } },
    { sources: ["/second.wav"], kind: "file", range: { start: 0, end: 5 } },
  ]);
});

test("invalid ranges never enqueue files or links", async () => {
  const h = harness();
  await flush();
  h.window.receive({ type: "files", data: ["/first.wav"] });
  await flush();
  h.element("#file-range-0-end").oninput({ target: { value: "12" } });
  await h.element("#enqueue-files").onclick();
  assert.equal(h.requests.some(r => r.action === "enqueue"), false);
  assert.equal(vm.runInContext("files.length", h.context), 1);
  typeURL(h, "https://example.org/audio");
  await h.element("#fetch-url").onclick();
  h.element("#url-range-start").oninput({ target: { value: "30" } });
  h.element("#url-range-end").oninput({ target: { value: "20" } });
  await h.element("#enqueue-url").onclick();
  assert.equal(h.requests.some(r => r.action === "enqueue"), false);
  assert.equal(h.element("#enqueue-url").disabled, false);
});

test("partially queued files are removed so retry cannot duplicate successful imports", async () => {
  const h = harness({ deferActions: ["enqueue"] });
  await flush();
  h.window.receive({ type: "files", data: ["/first.wav", "/second.wav"] });
  await flush();
  const adding = h.element("#enqueue-files").onclick();
  const first = h.requests.at(-1);
  h.window.receive({ type: "reply", data: { id: first.id, result: null } });
  await flush();
  const second = h.requests.at(-1);
  h.window.receive({ type: "reply", data: { id: second.id, error: "File moved" } });
  await adding;
  assert.equal(vm.runInContext("files.length", h.context), 1);
  assert.equal(vm.runInContext("files[0].path", h.context), "/second.wav");
});

test("URL ranges survive background updates and reset when the URL changes", async () => {
  const h = harness();
  await flush();
  typeURL(h, "https://example.org/audio");
  await h.element("#fetch-url").onclick();
  h.element("#url-range-start").oninput({ target: { value: "1:00.250" } });
  h.element("#url-range-end").oninput({ target: { value: "2:00" } });
  h.window.receive({ type: "jobs", data: [] });
  vm.runInContext('navigate("settings"); navigate("new")', h.context);
  assert.match(h.element("#url-details").innerHTML, /value="1:00.250"/);
  await h.element("#enqueue-url").onclick();
  const job = h.requests.find(r => r.action === "enqueue");
  assert.deepEqual(JSON.parse(JSON.stringify(job.data.range)), { start: 60.25, end: 120 });
  vm.runInContext('navigate("new")', h.context);
  typeURL(h, "https://example.org/second");
  assert.equal(vm.runInContext("urlRange.start", h.context), "");
});

test("an in-flight URL enqueue cannot duplicate work or clear a newer link", async () => {
  const h = harness({ deferActions: ["enqueue"] });
  await flush();
  typeURL(h, "https://example.org/first");
  await h.element("#fetch-url").onclick();
  const adding = h.element("#enqueue-url").onclick();
  const request = h.requests.at(-1);
  vm.runInContext('navigate("settings"); navigate("new")', h.context);
  await h.element("#enqueue-url").onclick();
  assert.equal(h.requests.filter(r => r.action === "enqueue").length, 1);
  typeURL(h, "https://example.org/second");
  h.window.receive({ type: "reply", data: { id: request.id, result: null } });
  await adding;
  assert.equal(vm.runInContext("urlDraft", h.context), "https://example.org/second");
  assert.equal(vm.runInContext("view", h.context), "new");
});

test("sign-in saves only browser choices and invalidates stale metadata", async () => {
  const h = harness({ deferActions: ["metadata"] });
  await flush();
  typeURL(h, "https://example.org/private");
  const lookup = h.element("#fetch-url").onclick();
  const request = h.requests.at(-1);
  h.element("#cookie-browser").value = "chrome";
  h.element("#cookie-profile").value = "Profile 1";
  await h.element("#save-url-auth").onclick();
  const saved = h.requests.find(r => r.action === "urlAuth");
  assert.deepEqual(JSON.parse(JSON.stringify(saved.data)), { browser: "chrome", profile: "Profile 1" });
  h.window.receive({ type: "reply", data: { id: request.id, result: { url: "https://example.org/private", title: "Old", duration: 60 } } });
  await lookup;
  assert.equal(vm.runInContext("urlMeta", h.context), null);
  h.element("#cookie-browser").value = "";
  h.element("#cookie-browser").onchange();
  await h.element("#save-url-auth").onclick();
  assert.equal(vm.runInContext("state.settings.urlAuth.browser", h.context), "");
  assert.equal(h.element("#cookie-profile").value, "");
});

test("sign-in failures expose the helper and preserve the link", async () => {
  const h = harness({ deferActions: ["metadata"] });
  await flush();
  typeURL(h, "https://example.org/private");
  const lookup = h.element("#fetch-url").onclick();
  const request = h.requests.at(-1);
  h.window.receive({ type: "reply", data: { id: request.id, error: "Sign in to confirm your age" } });
  await lookup;
  assert.equal(h.element("#url-sign-in").open, true);
  assert.equal(h.element("#url-error").hidden, false);
  assert.equal(vm.runInContext("urlDraft", h.context), "https://example.org/private");
});

test("timestamp parsing rejects malformed, negative, and nonfinite times", async () => {
  const h = harness();
  await flush();
  for (const raw of ["-1", "1:60", "1:60:00", "NaN", "Infinity", "1e2", "1:2:3:4", "1.1234", "9223372037"]) {
    assert.throws(() => vm.runInContext(`parseTimestamp(${JSON.stringify(raw)})`, h.context));
  }
  assert.equal(vm.runInContext('parseTimestamp("01:02:03.250")', h.context), 3723.25);
  assert.throws(() => vm.runInContext('readRange({start: "", end: "0"}, 60)', h.context));
});

test("Zen is saved by name and launches its own browser with a copyable URL", async () => {
  const h = harness();
  await flush();
  typeURL(h, "https://example.org/private?item=1");
  assert.equal(h.element("#sign-in-url").value, "https://example.org/private?item=1");
  h.element("#cookie-browser").value = "zen";
  h.element("#cookie-browser").onchange();
  h.element("#cookie-profile").value = "/profiles/zen/default";
  await h.element("#open-sign-in").onclick();
  await h.element("#copy-sign-in-url").onclick();
  await h.element("#save-url-auth").onclick();
  assert.deepEqual(JSON.parse(JSON.stringify(h.requests.find(r => r.action === "openSignIn").data)), {browser: "zen", url: "https://example.org/private?item=1"});
  assert.equal(h.requests.find(r => r.action === "copySignInURL").data, "https://example.org/private?item=1");
  assert.deepEqual(JSON.parse(JSON.stringify(h.requests.find(r => r.action === "urlAuth").data)), {browser: "zen", profile: "/profiles/zen/default"});
});

test("Dia and other browsers use chosen cookie exports and clear them when disabled", async () => {
  const h = harness();
  await flush();
  h.element("#cookie-browser").value = "dia";
  h.element("#cookie-browser").onchange();
  assert.equal(h.element("#cookie-file-field").hidden, false);
  assert.equal(h.element("#cookie-profile-field").hidden, true);
  await h.element("#save-url-auth").onclick();
  assert.equal(h.requests.some(r => r.action === "urlAuth"), false);
  await h.element("#choose-cookies").onclick();
  assert.equal(h.requests.at(-1).action, "pickCookies");
  h.window.receive({ type: "cookiesFile", data: ["/exports/cookies.txt"] });
  await h.element("#save-url-auth").onclick();
  assert.deepEqual(JSON.parse(JSON.stringify(h.requests.find(r => r.action === "urlAuth").data)), {browser: "dia", profile: "", cookiesFile: "/exports/cookies.txt"});
  h.element("#cookie-browser").value = "other";
  h.element("#cookie-browser").onchange();
  await h.element("#open-sign-in").onclick();
  assert.equal(h.requests.at(-1).data.browser, "other");
  h.element("#cookie-browser").value = "";
  h.element("#cookie-browser").onchange();
  await h.element("#save-url-auth").onclick();
  assert.equal(h.element("#cookie-file").value, "");
  assert.deepEqual(JSON.parse(JSON.stringify(h.requests.filter(r => r.action === "urlAuth").at(-1).data)), {browser: "", profile: ""});
});

test("fetched transcript names can be edited and survive navigation, updates, and refetching", async () => {
  const h = harness();
  await flush();
  typeURL(h, "https://example.org/lecture");
  await h.element("#fetch-url").onclick();
  assert.equal(vm.runInContext("urlTitle", h.context), "Lecture");
  h.element("#url-title").oninput({ target: { value: "My seminar & notes" } });
  h.window.receive({ type: "jobs", data: [] });
  vm.runInContext('navigate("settings"); navigate("new")', h.context);
  assert.match(h.element("#url-details").innerHTML, /value="My seminar &amp; notes"/);
  await h.element("#fetch-url").onclick();
  assert.equal(vm.runInContext("urlTitle", h.context), "My seminar & notes");
  await h.element("#enqueue-url").onclick();
  assert.equal(h.requests.find(r => r.action === "enqueue").data.title, "My seminar & notes");
  assert.equal(vm.runInContext("urlTitle", h.context), null);
});

test("blank names use the source title and new links reset the name draft", async () => {
  const h = harness();
  await flush();
  typeURL(h, "https://example.org/lecture");
  await h.element("#fetch-url").onclick();
  h.element("#url-title").oninput({ target: { value: "   " } });
  await h.element("#enqueue-url").onclick();
  assert.equal(h.requests.find(r => r.action === "enqueue").data.title, "");
  vm.runInContext('navigate("new")', h.context);
  typeURL(h, "https://example.org/second");
  await h.element("#fetch-url").onclick();
  assert.equal(vm.runInContext("urlTitle", h.context), "Lecture");
});

test("download progress appears in home and queue with bytes, speed, and ETA", async () => {
  const h = harness();
  await flush();
  const job = { id: "download", title: "Lecture", state: "downloading", progress: 0, download: { status: "downloading", downloaded: 1048576, total: 4194304, speed: 524288, eta: 6 } };
  h.window.receive({ type: "jobs", data: [job] });
  const home = h.element("#home-queue").innerHTML;
  assert.match(home, /value="0.25"/);
  assert.match(home, /25%/);
  assert.match(home, /1.0 MiB \/ 4.0 MiB/);
  assert.match(home, /512.0 KiB\/s/);
  assert.match(home, /ETA 00:06/);
  vm.runInContext('navigate("queue")', h.context);
  assert.match(h.element("#content").innerHTML, /Download progress/);
  job.download.total = 0;
  h.window.receive({ type: "jobs", data: [job] });
  const queue = h.element("#content").innerHTML;
  assert.match(queue, /<progress max="1"  aria-label="Download progress"/);
  assert.doesNotMatch(queue, /25%|0%/);
});

const pickerModels = {
  model: { id: "large-v3-turbo", path: "/turbo.bin" },
  modelCatalog: [
    { id: "large-v3-turbo", use: "Full Turbo", name: "Whisper Large v3 Turbo", bytes: 1624555275 },
    { id: "small-q5", use: "Low memory", name: "Whisper Small Q5", bytes: 190085487 },
    { id: "large-v3", use: "Higher accuracy", name: "Whisper Large v3", bytes: 3095033483 },
  ],
  modelInventory: { statuses: {
    "large-v3-turbo": { path: "/turbo.bin" },
    "small-q5": { path: "/small.bin", managed: true },
  }, download: {} },
};

test("a live model override is used once and leaves the default selected", async () => {
  const h = harness({ snapshot: pickerModels });
  await flush();
  assert.match(h.element("#home-recording").innerHTML, /value="large-v3-turbo" selected/);
  assert.doesNotMatch(h.element("#home-recording").innerHTML, /value="large-v3"/);
  h.element("#recording-model").onchange({ target: { value: "small-q5" } });
  await h.element("#start-recording").onclick();
  assert.equal(h.requests.find(r => r.action === "start").data, "small-q5");
  assert.match(h.element("#home-recording").innerHTML, /value="large-v3-turbo" selected/);
});

test("ask each time requires an installed model in file and URL review", async () => {
  const h = harness({ snapshot: { ...pickerModels, settings: { language: "auto", askModel: true } } });
  await flush();
  h.window.receive({ type: "files", data: ["/first.wav"] });
  await flush();
  await h.element("#enqueue-files").onclick();
  assert.equal(h.requests.some(r => r.action === "enqueue"), false);
  h.element("#files-model").onchange({ target: { value: "small-q5" } });
  await h.element("#enqueue-files").onclick();
  assert.equal(h.requests.find(r => r.action === "enqueue").data.modelId, "small-q5");

  vm.runInContext('navigate("new")', h.context);
  typeURL(h, "https://example.org/audio");
  await h.element("#fetch-url").onclick();
  await h.element("#enqueue-url").onclick();
  assert.equal(h.requests.filter(r => r.action === "enqueue").length, 1);
  h.element("#url-model").onchange({ target: { value: "large-v3-turbo" } });
  await h.element("#enqueue-url").onclick();
  assert.equal(h.requests.findLast(r => r.action === "enqueue").data.modelId, "large-v3-turbo");
});

test("model downloads and the default preference use the chosen model IDs", async () => {
  const h = harness({ snapshot: pickerModels });
  await flush();
  vm.runInContext('navigate("settings")', h.context);
  assert.match(h.element("#model-library").innerHTML, /data-model-download="large-v3"/);
  assert.match(h.element("#model-library").innerHTML, /data-model-remove="small-q5"/);
  await h.element("#model-default").onchange({ target: { value: "small-q5" } });
  assert.equal(h.requests.find(r => r.action === "modelPreference").data.id, "small-q5");
  await vm.runInContext('changeInstalledModel({ disabled: false }, "downloadModel", "large-v3")', h.context);
  assert.equal(h.requests.find(r => r.action === "downloadModel").data, "large-v3");
});

test("settings default to Taglish and mark Everyday & live as the default model", async () => {
  const model = { id: "large-v3-turbo-q5", use: "Everyday & live", name: "Whisper Large v3 Turbo Q5", bytes: 574041195 };
  const h = harness({ snapshot: { model: { id: model.id, state: "missing" }, settings: {}, defaultModelId: model.id, modelCatalog: [model] } });
  await flush();
  assert.match(h.element("#home-recording").innerHTML, /Download Whisper Large v3 Turbo Q5 \(547\.4 MiB\)/);
  vm.runInContext('navigate("settings")', h.context);
  assert.match(h.element("#content").innerHTML, /<option value="taglish">Taglish \(English and Tagalog\)<\/option>/);
  assert.equal(h.element("#language").value, "taglish");
  assert.match(h.element("#model-library").innerHTML, /<strong>Everyday &amp; live<\/strong><span class="model-tag">Default<\/span>/);
  await h.element("#save-settings").onclick();
  assert.equal(h.requests.findLast((r) => r.action === "settings").data.language, "taglish");
});


function showMeeting(h, id = "meeting", meeting = {}) {
  vm.runInContext(`selected = ${JSON.stringify({ id, type: "meeting", title: "Planning meeting", source: "Microphone + system audio", created: "2026-11-15", status: "completed", duration: 60, segments: [], corrections: {}, suggestions: {}, meeting })}; view = "session"; renderSession(); selected.segments.push({ id: "line", text: "We will ship on Friday." }); syncMeetingWorkspace()`, h.context);
}
const summaryReady = { available: true, ready: true, modelId: "qwen3.5-4b" };

test("meeting provider settings save an API key without putting it back in the dashboard", async () => {
  const providers = [
    { id: "gemini", name: "Google Gemini", defaultModel: "gemini-3.8-flash", hasKey: false },
    { id: "openai", name: "OpenAI", defaultModel: "gpt-5.4-mini", hasKey: false },
    { id: "anthropic", name: "Anthropic Claude", defaultModel: "claude-haiku-5-5", hasKey: false },
  ];
  const result = { provider: "gemini", model: "gemini-3.1-flash-lite", providers: [{ ...providers[0], hasKey: true }, ...providers.slice(1)] };
  const h = harness({ snapshot: { meetingProviders: { provider: "", model: "", providers } }, results: { meetingProvider: result } });
  await flush();
  vm.runInContext('navigate("settings")', h.context);
  assert.match(h.element("#content").innerHTML, /Local · Qwen3.5 4B Q4/);
  assert.match(h.element("#content").innerHTML, /Google Gemini/);
  assert.match(h.element("#content").innerHTML, /OpenAI/);
  assert.match(h.element("#content").innerHTML, /Anthropic Claude/);
  const select = h.element("#meeting-provider"), key = h.element("#meeting-provider-key");
  select.value = "anthropic";
  select.onchange();
  assert.match(h.element("#meeting-provider-key-help").textContent, /Claude Console API key and separate API billing/);
  select.value = "gemini";
  select.onchange();
  assert.equal(h.element("#save-meeting-provider").disabled, true);
  key.value = "test-key";
  key.oninput();
  h.element("#meeting-provider-model").value = result.model;
  await h.element("#save-meeting-provider").onclick();
  assert.equal(h.requests.findLast((r) => r.action === "meetingProvider").data.key, "test-key");
  assert.equal(key.value, "");
  assert.equal(h.element("#remove-meeting-provider-key").hidden, false);
  assert.match(h.element("#meeting-provider-key-help").textContent, /Keychain/);
});

test("meeting prompts can be edited, saved independently of recognition, and reset by mode", async () => {
  const defaults = { summary: "Default summary", enhancement: "Default enhancement" };
  const settings = { language: "en", meetingPrompts: { summary: "Saved summary" } };
  const h = harness({ snapshot: { meetingPromptDefaults: defaults, settings }, results: {
    meetingPrompts: ({ summary, enhancement }) => Object.fromEntries(
      Object.entries({ summary, enhancement }).filter(([id, text]) => text.trim() && text !== defaults[id]),
    ),
  } });
  await flush();
  vm.runInContext('navigate("settings")', h.context);
  const summary = h.element("#meeting-prompt-summary"), enhancement = h.element("#meeting-prompt-enhancement");
  const save = h.element("#save-meeting-prompts");
  assert.equal(summary.value, "Saved summary");
  assert.equal(enhancement.value, defaults.enhancement);
  assert.equal(save.disabled, true);
  enhancement.value = "Add dates.\nKeep the original wording.";
  enhancement.oninput();
  vm.runInContext('navigate("history"); navigate("settings")', h.context);
  assert.equal(enhancement.value, "Add dates.\nKeep the original wording.");
  const vocabulary = h.element("#context-prompt");
  vocabulary.value = "Unsaved vocabulary";
  await save.onclick();
  assert.equal(vocabulary.value, "Unsaved vocabulary");
  assert.equal(save.disabled, true);
  h.element("#reset-meeting-prompt-summary").onclick();
  assert.equal(summary.value, defaults.summary);
  await save.onclick();
  const payload = h.requests.findLast((request) => request.action === "meetingPrompts").data;
  assert.equal(payload.summary, defaults.summary);
  assert.equal(payload.enhancement, "Add dates.\nKeep the original wording.");
  assert.equal(vm.runInContext('state.settings.meetingPrompts.summary', h.context), undefined);
  assert.equal(save.disabled, true);
});

test("hosted enhancement sends a provider-scoped request only after confirmation", async () => {
  let allowed = false;
  const prompts = [];
  const modelId = "gemini-3.8-flash";
  const h = harness({ snapshot: { meetingSummary: { available: true, ready: true, provider: "gemini", providerName: "Google Gemini", modelId } },
    confirm: (prompt) => { prompts.push(prompt); return allowed; } });
  await flush();
  showMeeting(h);
  assert.equal(h.element("#summarize-meeting").textContent, "Enhance with Google Gemini");
  await h.element("#summarize-meeting").onclick();
  assert.match(prompts[0], /send this meeting’s notes and transcript text to Google Gemini/);
  assert.equal(h.requests.some((r) => r.action === "summarizeMeeting"), false);
  allowed = true;
  await h.element("#summarize-meeting").onclick();
  const { data } = h.requests.findLast((r) => r.action === "summarizeMeeting");
  assert.equal(data.id, "meeting");
  assert.equal(data.provider, "gemini");
  assert.equal(data.model, modelId);
});

test("a failed Gemini enhancement explains what happened in both notebook tabs", async () => {
  const h = harness({ deferActions: ["summarizeMeeting"], snapshot: { meetingSummary: {
    available: true, ready: true, provider: "gemini", providerName: "Google Gemini", modelId: "gemini-3.8-flash",
  } } });
  await flush();
  showMeeting(h);
  const generate = h.element("#summarize-meeting").onclick();
  await flush();
  const request = h.requests.findLast((r) => r.action === "summarizeMeeting");
  h.window.receive({ type: "reply", data: { id: request.id, error: "Google Gemini reached its output limit before finishing the notes." } });
  await generate;
  const status = h.element("#meeting-summary-status");
  assert.match(status.textContent, /Enhancement failed.*Google Gemini reached its output limit/);
  h.element("#enhanced-notes-tab").onclick();
  assert.match(status.textContent, /Enhancement failed.*Google Gemini reached its output limit/);
  assert.equal(h.requests.filter((r) => r.action === "summarizeMeeting").length, 1);
});

test("ChatGPT plan sign-in loads account models and saves the selected account", async () => {
  const providers = [{ id: "chatgpt", name: "ChatGPT plan" }];
  const accounts = [{ id: "account-1", label: "first@example.com · Account 1", authorized: true, signedIn: true,
    models: [{ id: "first-model", name: "First model" }, { id: "second-model", name: "Second model" }] }];
  const alerts = [];
  const h = harness({ snapshot: { meetingProviders: { providers } }, alert: (text) => alerts.push(text), results: {
    chatGPTAccount: { selected: "account-1", welcome: true, providers, chatGPTAccounts: accounts },
    meetingProvider: { provider: "chatgpt", account: "account-1", model: "second-model", providers, chatGPTAccounts: accounts },
  } });
  await flush();
  vm.runInContext('navigate("settings")', h.context);
  h.element("#meeting-provider").value = "chatgpt";
  h.element("#meeting-provider").onchange();
  assert.match(h.element("#content").innerHTML, /Continue with ChatGPT/);
  assert.equal(h.element("#meeting-provider-fields").hidden, true);
  assert.equal(h.element("#save-meeting-provider").disabled, true);
  await h.element("#connect-chatgpt").onclick();
  assert.equal(h.requests.findLast((r) => r.action === "chatGPTAccount").data.operation, "signIn");
  assert.match(alerts[0], /Your ChatGPT plan is connected/);
  assert.equal(h.element("#meeting-chatgpt-account").value, "account-1");
  assert.equal(h.element("#meeting-chatgpt-model").value, "first-model");
  assert.match(h.element("#meeting-chatgpt-model").innerHTML, /Second model/);
  h.element("#meeting-chatgpt-model").value = "second-model";
  await h.element("#save-meeting-provider").onclick();
  const { data } = h.requests.findLast((r) => r.action === "meetingProvider");
  assert.deepEqual(JSON.parse(JSON.stringify(data)), { provider: "chatgpt", account: "account-1", model: "second-model", key: "" });
});

test("ChatGPT enhancement confirms the account and gives usage limits a manage action", async () => {
  const summary = { available: true, ready: true, provider: "chatgpt", providerName: "ChatGPT", modelId: "first-model",
    account: "account-1", accountLabel: "first@example.com · Account 1" };
  const h = harness({ snapshot: { meetingSummary: summary }, confirm: (message) => /plan or credits for first@example\.com/.test(message) });
  await flush();
  showMeeting(h);
  await h.element("#summarize-meeting").onclick();
  assert.equal(h.requests.findLast((r) => r.action === "summarizeMeeting").data.account, "account-1");
  h.window.receive({ type: "meetingSummary", data: { ...summary, ready: false, usageLimited: true } });
  assert.equal(h.element("#summarize-meeting").disabled, true);
  assert.match(h.element("#meeting-summary-status").textContent, /usage limit reached/);
  await h.element("#manage-meeting-usage").onclick();
  assert.equal(h.requests.at(-1).action, "manageChatGPTUsage");
});

test("start meeting uses its own native action and the selected recording model", async () => {
  const h = harness({ snapshot: pickerModels });
  await flush();
  h.element("#recording-model").onchange({ target: { value: "small-q5" } });
  await h.element("#start-meeting").onclick();
  assert.equal(h.requests.find(r => r.action === "startMeeting").data, "small-q5");
  assert.equal(h.requests.some(r => r.action === "start"), false);
});

test("meeting notes preserve a local draft through native updates and navigation", async () => {
  const h = harness({ snapshot: { meetingSummary: summaryReady } });
  await flush();
  showMeeting(h, "meeting", { notes: "Saved note" });
  h.element("#meeting-notes").oninput({ target: { value: "My unsaved note" } });
  h.element("#meeting-notes").value = "My unsaved note";
  const page = h.element("#content").innerHTML;
  h.window.receive({ type: "meeting", data: { sessionId: "meeting", meeting: { notes: "Saved note", summary: "New summary" } } });
  h.window.receive({ type: "meetingSummary", data: { ...summaryReady, busy: true, sessionId: "another" } });
  assert.equal(h.element("#content").innerHTML, page);
  assert.equal(h.element("#meeting-notes").value, "My unsaved note");
  assert.equal(h.element("#summarize-meeting").disabled, true);
  vm.runInContext('navigate("history")', h.context);
  showMeeting(h, "meeting", { notes: "Saved note" });
  assert.equal(h.element("#meeting-notes").value, "My unsaved note");
  assert.match(h.element("#meeting-notes-status").textContent, /Unsaved changes/);
  const savedDraft = h.requests.findLast(r => r.action === "preference" && r.data.key === "meetingDrafts");
  assert.equal(JSON.parse(savedDraft.data.value).meeting, "My unsaved note");
});

test("meeting drafts restore after reopening and save failures keep them editable", async () => {
  const h = harness({ deferActions: ["saveMeetingNotes"], preferences: { meetingDrafts: JSON.stringify({ meeting: "Restored draft" }) } });
  await flush();
  showMeeting(h, "meeting", { notes: "Saved" });
  assert.equal(h.element("#meeting-notes").value, "Restored draft");
  const saving = h.element("#save-meeting-notes").onclick();
  const request = h.requests.at(-1);
  assert.equal(request.action, "saveMeetingNotes");
  assert.equal(request.data.notes, "Restored draft");
  h.window.receive({ type: "reply", data: { id: request.id, error: "Disk is full" } });
  await saving;
  assert.match(h.element("#error span").textContent, /Disk is full/);
  assert.equal(h.element("#save-meeting-notes").disabled, false);
  assert.equal(vm.runInContext('meetingDrafts.get("meeting")', h.context), "Restored draft");
});

test("saving notes never clears text entered while the save is in flight", async () => {
  const h = harness({ deferActions: ["saveMeetingNotes"] });
  await flush();
  showMeeting(h);
  h.element("#meeting-notes").oninput({ target: { value: "First draft" } });
  const saving = h.element("#save-meeting-notes").onclick();
  const request = h.requests.at(-1);
  h.element("#meeting-notes").oninput({ target: { value: "Newer draft" } });
  h.window.receive({ type: "reply", data: { id: request.id, result: { notes: "First draft" } } });
  await saving;
  assert.equal(vm.runInContext('selected.meeting.notes', h.context), "First draft");
  assert.equal(vm.runInContext('meetingDrafts.get("meeting")', h.context), "Newer draft");
  assert.match(h.element("#meeting-notes-status").textContent, /Unsaved changes/);
});

test("summary saves notes first, prevents duplicates, and does not update a different session", async () => {
  const h = harness({ snapshot: { meetingSummary: summaryReady }, deferActions: ["saveMeetingNotes", "summarizeMeeting"] });
  await flush();
  showMeeting(h);
  h.element("#meeting-notes").oninput({ target: { value: "Important context" } });
  const generating = h.element("#summarize-meeting").onclick();
  const save = h.requests.at(-1);
  assert.equal(save.action, "saveMeetingNotes");
  assert.equal(h.requests.some(r => r.action === "summarizeMeeting"), false);
  h.window.receive({ type: "reply", data: { id: save.id, result: { notes: "Important context" } } });
  await flush();
  const summary = h.requests.at(-1);
  assert.equal(summary.action, "summarizeMeeting");
  assert.equal(summary.data.id, "meeting");
  assert.equal(summary.data.provider, "");
  await h.element("#summarize-meeting").onclick();
  assert.equal(h.requests.filter(r => r.action === "summarizeMeeting").length, 1);
  showMeeting(h, "another", { notes: "Other notes", summary: "Other summary" });
  h.window.receive({ type: "reply", data: { id: summary.id, result: { notes: "Important context", summary: "Original meeting summary" } } });
  await generating;
  assert.equal(vm.runInContext('selected.meeting.summary', h.context), "Other summary");
  h.element("#enhanced-notes-tab").onclick();
  assert.equal(h.element("#meeting-summary").innerHTML.trim(), "<p>Other summary</p>");
});

test("summary cancellation survives navigation and generated HTML stays text", async () => {
  const h = harness({ snapshot: { meetingSummary: summaryReady } });
  await flush();
  showMeeting(h, "meeting", { summary: "<img src=x onerror=alert(1)>\n## Decisions" });
  h.element("#enhanced-notes-tab").onclick();
  assert.match(h.element("#meeting-summary").innerHTML, /<p>&lt;img src=x onerror=alert\(1\)&gt;<\/p>\s*<h3>Decisions<\/h3>/);
  h.window.receive({ type: "meetingSummary", data: { ...summaryReady, busy: true, sessionId: "meeting" } });
  assert.equal(h.element("#cancel-meeting-summary").hidden, false);
  vm.runInContext('navigate("history")', h.context);
  showMeeting(h);
  assert.equal(h.element("#cancel-meeting-summary").hidden, false);
  await h.element("#cancel-meeting-summary").onclick();
  assert.equal(h.requests.findLast(r => r.action === "cancelMeetingSummary").data, "meeting");
});

test("meeting sharing saves the latest notes and keeps transcript export separate", async () => {
  const h = harness({ results: { saveMeetingNotes: data => ({ notes: data.notes }) } });
  await flush();
  showMeeting(h);
  h.element("#meeting-notes").oninput({ target: { value: "Share this note" } });
  await h.element("#copy-meeting").onclick();
  const copy = h.requests.findLast(r => r.action === "copy");
  assert.deepEqual(JSON.parse(JSON.stringify(copy.data)), { id: "meeting", format: "md", mode: "meeting-original" });
  await h.element("#copy-transcript").onclick();
  const transcript = h.requests.findLast(r => r.action === "copy");
  assert.equal(transcript.data.format, "txt");
  assert.notEqual(transcript.data.mode, "meeting");
});

test("meeting model setup downloads the summary model and stale summaries ask for regeneration", async () => {
  const h = harness({ snapshot: { meetingSummary: { ...summaryReady, ready: false } } });
  await flush();
  showMeeting(h);
  assert.equal(h.element("#summarize-meeting").disabled, true);
  assert.equal(h.element("#meeting-summary-setup").hidden, false);
  await h.element("#download-meeting-model").onclick();
  assert.equal(h.requests.findLast(r => r.action === "downloadModel").data, summaryReady.modelId);
  h.window.receive({ type: "meetingSummary", data: summaryReady });
  assert.equal(h.element("#summarize-meeting").disabled, false);
  h.window.receive({ type: "meeting", data: { sessionId: "meeting", meeting: { summary: "Old summary", summaryStale: true } } });
  assert.match(h.element("#meeting-summary-status").textContent, /Re-enhance/);
  h.window.receive({ type: "live", data: { active: true, sessionId: "different", state: "recording", started: new Date().toISOString() } });
  assert.equal(h.element("#summarize-meeting").disabled, true);
});


test("background transcript capture never scrolls the notes page and only follows an open drawer", async () => {
  const h = harness();
  await flush();
  showMeeting(h);
  vm.runInContext(`
    const originalQuery = document.querySelector;
    document.querySelector = selector => selector === ".segment textarea" ? null : originalQuery(selector);
    insertSegment = () => {};
    document.activeElement = document.querySelector("#meeting-notes");
    content.scrollTop = 25; content.scrollHeight = 1000;
    document.querySelector("#transcript-scroll").scrollTop = 10;
    document.querySelector("#transcript-scroll").scrollHeight = 700;
  `, h.context);
  h.window.receive({ type: "segment", data: { sessionId: "meeting", id: "new-line", text: "Next topic", start: 1 } });
  assert.equal(h.element("#content").scrollTop, 25);
  assert.equal(h.element("#transcript-scroll").scrollTop, 10);
  vm.runInContext('document.activeElement = null', h.context);
  h.window.receive({ type: "segment", data: { sessionId: "meeting", id: "next-line", text: "More discussion", start: 2 } });
  assert.equal(h.element("#transcript-scroll").scrollTop, 10);
  h.element("#toggle-transcript").onclick();
  h.window.receive({ type: "segment", data: { sessionId: "meeting", id: "visible-line", text: "Drawer follows", start: 3 } });
  assert.equal(h.element("#transcript-scroll").scrollTop, 700);
  assert.equal(h.element("#content").scrollTop, 25);
  vm.runInContext('document.activeElement = document.querySelector("#enhanced-meeting-notes"); document.querySelector("#transcript-scroll").scrollTop = 20', h.context);
  h.window.receive({ type: "segment", data: { sessionId: "meeting", id: "editing-line", text: "Enhanced editor focused", start: 4 } });
  assert.equal(h.element("#transcript-scroll").scrollTop, 20);
});

test("summary waits for changed notes and never sends a stale draft to generation", async () => {
  const h = harness({ snapshot: { meetingSummary: summaryReady }, deferActions: ["saveMeetingNotes"] });
  await flush();
  showMeeting(h);
  h.element("#meeting-notes").oninput({ target: { value: "Before save" } });
  const generating = h.element("#summarize-meeting").onclick();
  const save = h.requests.at(-1);
  h.element("#meeting-notes").oninput({ target: { value: "Typed during save" } });
  h.window.receive({ type: "reply", data: { id: save.id, result: { notes: "Before save" } } });
  await generating;
  assert.equal(h.requests.some(r => r.action === "summarizeMeeting"), false);
  assert.equal(vm.runInContext('meetingDrafts.get("meeting")', h.context), "Typed during save");
  assert.match(h.element("#meeting-summary-status").textContent, /notes changed while saving/);
});


test("summary rendering supports headings and lists while escaping every content line", async () => {
  const h = harness();
  await flush();
  const text = "## Summary\nA <b>plan</b> & a [link](javascript:alert(1))\n\n## Next steps\n- Ship <script>alert(1)</script>\n- Owner: Sam";
  const html = vm.runInContext(`notebookMarkdown.render(${JSON.stringify(text)})`, h.context);
  assert.equal(html.trim().replaceAll(">\n<", "><"), '<h3>Summary</h3><p>A &lt;b&gt;plan&lt;/b&gt; &amp; a [link](javascript:alert(1))</p><h3>Next steps</h3><ul><li>Ship &lt;script&gt;alert(1)&lt;/script&gt;</li><li>Owner: Sam</li></ul>');
  assert.doesNotMatch(html, /<a |<b>|<script>/);
});

test("expected summary cancellation leaves a calm status and preserves notes", async () => {
  const h = harness({ snapshot: { meetingSummary: summaryReady }, deferActions: ["summarizeMeeting"] });
  await flush();
  showMeeting(h, "meeting", { notes: "Saved notes" });
  h.element("#error").hidden = true;
  const generating = h.element("#summarize-meeting").onclick();
  await flush();
  const request = h.requests.findLast(r => r.action === "summarizeMeeting");
  h.window.receive({ type: "reply", data: { id: request.id, error: "generate meeting summary: context canceled" } });
  await generating;
  assert.equal(h.element("#error").hidden, true);
  assert.match(h.element("#meeting-summary-status").textContent, /Enhancement canceled/);
  assert.equal(vm.runInContext('selected.meeting.notes', h.context), "Saved notes");
});


test("older meeting events and RPC replies cannot overwrite newer notes or summaries", async () => {
  const h = harness({ snapshot: { meetingSummary: summaryReady }, deferActions: ["saveMeetingNotes", "summarizeMeeting"] });
  await flush();
  showMeeting(h, "meeting", { notes: "Initial notes", summary: "Initial summary", revision: 1 });
  h.element("#meeting-notes").oninput({ target: { value: "Saved by this request" } });
  const saving = h.element("#save-meeting-notes").onclick();
  const save = h.requests.at(-1);
  h.window.receive({ type: "meeting", data: { sessionId: "meeting", meeting: { notes: "Newest notes", summary: "Newest summary", revision: 3 } } });
  h.window.receive({ type: "reply", data: { id: save.id, result: { notes: "Saved by this request", summary: "Initial summary", revision: 2 } } });
  await saving;
  assert.equal(vm.runInContext('selected.meeting.notes', h.context), "Newest notes");
  assert.equal(vm.runInContext('selected.meeting.summary', h.context), "Newest summary");
  const generating = h.element("#summarize-meeting").onclick();
  await flush();
  const summary = h.requests.at(-1);
  h.window.receive({ type: "meeting", data: { sessionId: "meeting", meeting: { notes: "Edited during generation", summary: "New summary", summaryStale: true, revision: 5 } } });
  h.window.receive({ type: "reply", data: { id: summary.id, result: { notes: "Newest notes", summary: "New summary", revision: 4 } } });
  await generating;
  h.window.receive({ type: "meeting", data: { sessionId: "meeting", meeting: { notes: "Old event", summary: "Old event summary", revision: 1 } } });
  assert.equal(vm.runInContext('selected.meeting.notes', h.context), "Edited during generation");
  assert.equal(vm.runInContext('selected.meeting.revision', h.context), 5);
  assert.match(h.element("#meeting-summary-status").textContent, /Re-enhance/);
});


test("meeting opens the notebook first with background transcript and no fixed transcript toolbar", async () => {
  const h = harness();
  await flush();
  showMeeting(h);
  assert.equal(h.element("#your-notes-panel").hidden, false);
  assert.equal(h.element("#enhanced-notes-panel").hidden, true);
  assert.equal(h.element("#transcript-drawer").hidden, true);
  assert.equal(h.element("#session-tools").hidden, true);
  const html = h.element("#content").innerHTML;
  assert.ok(html.indexOf('class="meeting-notebook"') < html.indexOf('class="meeting-transcript"'));
  assert.ok(html.indexOf('id="transcript-drawer"') < html.indexOf('id="copy-transcript"'));
});

test("raw transcription opens its transcript visibly and keeps its original workflow first", async () => {
  const raw = { id: "raw", title: "Interview audio", type: "file", source: "/audio/interview.wav", created: "2026-11-15", segments: [] };
  const h = harness({ results: { get: raw } });
  await flush();
  await vm.runInContext('openSession("raw")', h.context);
  assert.equal(vm.runInContext('isMeetingSession()', h.context), false);
  assert.equal(h.element("#transcript-drawer").hidden, false);
  const html = h.element("#content").innerHTML;
  assert.ok(html.indexOf('class="meeting-transcript"') < html.indexOf('class="meeting-notebook"'));
});

test("transcript Back returns to its origin and restored transcripts return to history", async () => {
  const raw = { id: "raw", title: "Interview audio", type: "file", source: "/audio/interview.wav", created: "2026-11-15", segments: [] };
  const h = harness({ results: { get: raw } });
  await flush();
  for (const origin of ["new", "queue", "history"]) {
    await vm.runInContext(`navigate(${JSON.stringify(origin)}); openSession("raw")`, h.context);
    await vm.runInContext('openSession("raw")', h.context);
    assert.match(h.element("#content").innerHTML, /id="back-from-transcript"/);
    h.element("#back-from-transcript").onclick();
    assert.equal(vm.runInContext('view', h.context), origin);
    assert.equal(vm.runInContext('storage.getItem("selectedSession")', h.context), null);
  }
  const restored = harness({ snapshot: { sessions: [raw] }, results: { get: raw }, preferences: { selectedSession: "raw" } });
  await flush();
  assert.equal(vm.runInContext('view', restored.context), "session");
  restored.element("#back-from-transcript").onclick();
  assert.equal(vm.runInContext('view', restored.context), "history");
});

test("enhancement opens enhanced notes after completion while preserving the original", async () => {
  const h = harness({ snapshot: { meetingSummary: summaryReady }, deferActions: ["summarizeMeeting"] });
  await flush();
  showMeeting(h, "meeting", { notes: "Original outline", revision: 1 });
  const generating = h.element("#summarize-meeting").onclick();
  await flush();
  const request = h.requests.findLast(r => r.action === "summarizeMeeting");
  h.window.receive({ type: "reply", data: { id: request.id, result: { notes: "Original outline", summary: "Original outline\n- Added detail", revision: 2 } } });
  await generating;
  assert.equal(h.element("#your-notes-panel").hidden, true);
  assert.equal(h.element("#enhanced-notes-panel").hidden, false);
  assert.equal(h.element("#meeting-notes").value, "Original outline");
  assert.equal(h.element("#summarize-meeting").textContent, "Re-enhance notes");
  h.element("#your-notes-tab").onclick();
  assert.equal(h.element("#your-notes-panel").hidden, false);
  assert.equal(h.element("#meeting-notes").value, "Original outline");
});

test("enhancement completion never hides active typing or a newer original draft", async () => {
  for (const focused of [true, false]) {
    const h = harness({ snapshot: { meetingSummary: summaryReady }, deferActions: ["summarizeMeeting"] });
    await flush();
    showMeeting(h, "meeting", { notes: "Original outline", revision: 1 });
    const generating = h.element("#summarize-meeting").onclick();
    await flush();
    const request = h.requests.findLast(r => r.action === "summarizeMeeting");
    if (focused) vm.runInContext('document.activeElement = document.querySelector("#meeting-notes")', h.context);
    else h.element("#meeting-notes").oninput({ target: { value: "Original plus a newer thought" } });
    h.window.receive({ type: "reply", data: { id: request.id, result: { notes: "Original outline", summary: "Enhanced detail", revision: 2 } } });
    await generating;
    assert.equal(h.element("#your-notes-panel").hidden, false);
    assert.equal(h.element("#enhanced-notes-panel").hidden, true);
    assert.match(h.element("#enhanced-notes-tab").textContent, /ready/);
    assert.match(h.element("#meeting-summary-status").textContent, /Enhanced notes are ready/);
    if (!focused) assert.equal(vm.runInContext('meetingDrafts.get("meeting")', h.context), "Original plus a newer thought");
  }
});

test("enhanced drafts survive tab switches, navigation and updates independently of original notes", async () => {
  const h = harness({ preferences: { enhancedMeetingDrafts: JSON.stringify({ meeting: "Restored enhanced draft" }) } });
  await flush();
  showMeeting(h, "meeting", { notes: "Original", summary: "Saved enhancement", revision: 1 });
  h.element("#meeting-notes").oninput({ target: { value: "Original draft" } });
  h.element("#enhanced-notes-tab").onclick();
  assert.equal(h.element("#enhanced-meeting-notes").hidden, false);
  assert.equal(h.element("#enhanced-meeting-notes").value, "Restored enhanced draft");
  h.element("#enhanced-meeting-notes").oninput({ target: { value: "Enhanced draft v2" } });
  h.element("#your-notes-tab").onclick();
  h.window.receive({ type: "meeting", data: { sessionId: "meeting", meeting: { notes: "Saved elsewhere", summary: "Other enhancement", revision: 2 } } });
  vm.runInContext('navigate("history")', h.context);
  showMeeting(h, "meeting", { notes: "Saved elsewhere", summary: "Other enhancement", revision: 2 });
  assert.equal(h.element("#meeting-notes").value, "Original draft");
  h.element("#enhanced-notes-tab").onclick();
  assert.equal(h.element("#enhanced-meeting-notes").value, "Enhanced draft v2");
  assert.equal(h.element("#enhanced-meeting-notes").hidden, false);
  const savedDraft = h.requests.findLast(r => r.action === "preference" && r.data.key === "enhancedMeetingDrafts");
  assert.equal(JSON.parse(savedDraft.data.value).meeting, "Enhanced draft v2");
});

test("saving enhanced edits preserves the source and newer text typed during the save", async () => {
  const h = harness({ snapshot: { meetingSummary: summaryReady }, deferActions: ["saveEnhancedNotes"] });
  await flush();
  showMeeting(h, "meeting", { notes: "Original source", summary: "Saved enhanced notes", revision: 1 });
  h.element("#enhanced-notes-tab").onclick();
  h.element("#edit-enhanced-notes").onclick();
  h.element("#enhanced-meeting-notes").oninput({ target: { value: "Edited enhancement" } });
  const saving = h.element("#save-enhanced-notes").onclick();
  const request = h.requests.at(-1);
  assert.equal(request.action, "saveEnhancedNotes");
  assert.deepEqual(JSON.parse(JSON.stringify(request.data)), { id: "meeting", text: "Edited enhancement" });
  h.element("#enhanced-meeting-notes").oninput({ target: { value: "Newer enhancement draft" } });
  h.window.receive({ type: "reply", data: { id: request.id, result: { notes: "Original source", summary: "Edited enhancement", revision: 2 } } });
  await saving;
  assert.equal(vm.runInContext('selected.meeting.notes', h.context), "Original source");
  assert.equal(vm.runInContext('enhancedMeetingDrafts.get("meeting")', h.context), "Newer enhancement draft");
  assert.equal(h.element("#enhanced-meeting-notes").hidden, false);
  assert.equal(h.element("#save-enhanced-notes").disabled, false);
});

test("dirty enhanced notes block regeneration and may be discarded without touching source notes", async () => {
  const h = harness({ snapshot: { meetingSummary: summaryReady } });
  await flush();
  showMeeting(h, "meeting", { notes: "Original source", summary: "Saved enhanced notes" });
  h.element("#enhanced-notes-tab").onclick();
  h.element("#edit-enhanced-notes").onclick();
  h.element("#enhanced-meeting-notes").oninput({ target: { value: "Unsaved enhanced edits" } });
  assert.equal(h.element("#summarize-meeting").disabled, true);
  assert.match(h.element("#meeting-summary-status").textContent, /Save or discard/);
  await h.element("#summarize-meeting").onclick();
  assert.equal(h.requests.some(r => r.action === "summarizeMeeting"), false);
  h.element("#discard-enhanced-notes").onclick();
  assert.equal(vm.runInContext('enhancedMeetingDrafts.has("meeting")', h.context), false);
  assert.equal(vm.runInContext('selected.meeting.notes', h.context), "Original source");
  assert.equal(h.element("#summarize-meeting").disabled, false);
  h.window.receive({ type: "meetingSummary", data: { ...summaryReady, busy: true, sessionId: "meeting" } });
  assert.equal(h.element("#enhanced-meeting-notes").disabled, true);
  assert.equal(h.element("#save-enhanced-notes").disabled, true);
  assert.notEqual(h.element("#meeting-notes").disabled, true);
});

test("sharing exports the visible notes version after saving that version", async () => {
  const h = harness({ results: { saveEnhancedNotes: data => ({ notes: "Original source", summary: data.text, revision: 2 }) } });
  await flush();
  showMeeting(h, "meeting", { notes: "Original source", summary: "Original enhancement", revision: 1 });
  await h.element("#copy-meeting").onclick();
  assert.equal(h.requests.findLast(r => r.action === "copy").data.mode, "meeting-original");
  h.element("#enhanced-notes-tab").onclick();
  h.element("#edit-enhanced-notes").onclick();
  h.element("#enhanced-meeting-notes").oninput({ target: { value: "Share my edited enhancement" } });
  await h.element("#copy-meeting").onclick();
  assert.equal(h.requests.findLast(r => r.action === "saveEnhancedNotes").data.text, "Share my edited enhancement");
  assert.equal(h.requests.findLast(r => r.action === "copy").data.mode, "meeting");
  assert.equal(vm.runInContext('selected.meeting.notes', h.context), "Original source");
});

test("Command-F and Control-F open the transcript drawer before focusing search", async () => {
  const h = harness();
  await flush();
  showMeeting(h);
  let focused = false;
  h.element("#transcript-search").focus = () => { focused = true; assert.equal(h.element("#transcript-drawer").hidden, false); };
  for (const key of ["metaKey", "ctrlKey"]) {
    vm.runInContext('setTranscriptVisible(false)', h.context);
    let prevented = false;
    h.listeners.get("keydown")({ [key]: true, key: "f", preventDefault() { prevented = true; } });
    assert.equal(prevented, true);
    assert.equal(focused, true);
  }
});

test("enhanced preview preserves source heading levels, line breaks and nested additions safely", async () => {
  const h = harness();
  await flush();
  const outline = "# Launch <plan>\nFirst source line\nSecond source line\n\n## Timeline\n- Ship Friday\n  - Sam owns the <checklist>\n  - [Link](javascript:alert(1)) stays text\n- Ask about budget\n* Budget\n  - Cap & review\n+ Risks\n  - Escaped <risk>\n### Open questions\nNeed approval?";
  const html = vm.runInContext(`notebookMarkdown.render(${JSON.stringify(outline)})`, h.context);
  assert.match(html, /<h2>Launch &lt;plan&gt;<\/h2>\s*<p>First source line<br>\s*Second source line<\/p>\s*<h3>Timeline<\/h3>/);
  assert.match(html, /<li>Ship Friday\s*<ul>\s*<li>Sam owns the &lt;checklist&gt;<\/li>\s*<li>\[Link\]\(javascript:alert\(1\)\) stays text<\/li>/);
  assert.match(html, /<li>Budget\s*<ul>\s*<li>Cap &amp; review<\/li>/);
  assert.match(html, /<h4>Open questions<\/h4>\s*<p>Need approval\?<\/p>/);
  assert.doesNotMatch(html, /<a |<plan>|<checklist>/);
});

test("Your notes previews the unsaved Markdown draft and saves its source when copied", async () => {
  const h = harness({ results: { saveMeetingNotes: ({ notes }) => ({ notes }) } });
  await flush();
  showMeeting(h, "meeting", { notes: "Saved plain text", summary: "**Enhanced**" });
  const editor = h.element("#meeting-notes"), toggle = h.element("#preview-meeting-notes"), preview = h.element("#meeting-notes-preview");
  const draft = ["# Agenda", "", "**Priority** and *context*", "", "1. Check coverage", "", "- [x] **Review**", "- [ ] Ship", "", "| Owner | Due |", "| --- | --- |", "| Sam | Friday |", "", "> Confirmed", "", "```text", "<literal>", "```", "", "[Checklist](https://example.com/checklist)", "![Diagram](https://example.com/image.png)"].join("\n");
  editor.value = draft;
  editor.oninput({ target: editor });
  toggle.onclick();
  assert.equal(editor.hidden, true);
  assert.equal(preview.hidden, false);
  assert.equal(toggle.textContent, "Edit Markdown");
  for (const part of ["<h2>Agenda</h2>", "<strong>Priority</strong>", "<em>context</em>", "<ol>", "checked disabled", 'aria-label="Open task"', "<strong>Review</strong>", "<table>", "<blockquote>", "<pre><code", "&lt;literal&gt;", '<a href="https://example.com/checklist">Checklist</a>', "Diagram"]) {
    assert.ok(preview.innerHTML.includes(part), `Preview is missing ${part}`);
  }
  assert.doesNotMatch(preview.innerHTML, /<img|<literal>/);
  assert.equal(h.requests.some((request) => request.action === "saveMeetingNotes"), false);

  let prevented = false;
  await preview.onclick({ target: { closest: () => ({ getAttribute: () => "https://example.com/checklist" }) }, preventDefault: () => { prevented = true; } });
  assert.equal(prevented, true);
  assert.equal(h.requests.findLast((request) => request.action === "openSignIn").data.url, "https://example.com/checklist");

  h.element("#enhanced-notes-tab").onclick();
  assert.match(h.element("#meeting-summary").innerHTML, /<strong>Enhanced<\/strong>/);
  h.element("#your-notes-tab").onclick();
  assert.equal(preview.hidden, false);
  await h.element("#copy-meeting").onclick();
  assert.equal(h.requests.findLast((request) => request.action === "saveMeetingNotes").data.notes, draft);
  assert.equal(h.requests.findLast((request) => request.action === "copy").data.format, "md");
  toggle.onclick();
  assert.equal(editor.value, draft);
  assert.equal(editor.hidden, false);
});
