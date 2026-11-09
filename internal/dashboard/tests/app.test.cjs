const { test } = require("node:test");
const assert = require("node:assert/strict");
const { readFileSync } = require("node:fs");
const { join } = require("node:path");
const vm = require("node:vm");

// Exercise the production event handlers without a browser-specific File.path.
// Native pasteboard resolution is mocked at the bridge boundary.
function harness({ deferActions = [], snapshot: overrides = {} } = {}) {
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
  const window = { addEventListener() {}, getSelection() { return ""; }, confirm() { return true; } };
  const document = { querySelector: element, querySelectorAll() { return []; },
    body: element("body"), documentElement: element("html"), addEventListener(name, callback) { listeners.set(name, callback); } };
  window.webkit = { messageHandlers: { tala: { postMessage(request) {
    requests.push(request);
    if (deferActions.includes(request.action)) return;
    if (request.action === "clearQueue") snapshot.jobs = [];
    const result = request.action === "snapshot" ? snapshot
      : request.action === "dependencies" ? snapshot.dependencies
      : request.action === "probe" ? { duration: 11, format: "mp3" }
      : request.action === "metadata" ? { url: request.data, title: "Lecture", duration: 125 } : null;
    queueMicrotask(() => window.receive({ type: "reply", data: { id: request.id, result } }));
  } } } };
  const context = vm.createContext({ window, document, setInterval() {}, setTimeout() {}, clearTimeout() {} });
  vm.runInContext(readFileSync(join(__dirname, "../assets/app.js"), "utf8"), context);
  return { context, window, listeners, requests, element };
}
const flush = () => new Promise(setImmediate);

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
