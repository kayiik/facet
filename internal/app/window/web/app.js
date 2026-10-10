// The Facet App's window. It renders what the agent engine reports and sends
// what the person does; the App behind it holds every decision. Besides the
// conversation it has the screens that need no model: projects and their
// media, technical review and the person's decision, and readiness.
"use strict";

const SESSION = "main";

// The App opens /#t=<token>. A screen's address is #/<screen>, and an
// address may carry both (#/projects?t=<token>). The token is kept for the
// tab and taken out of the address bar.
let token = (() => {
  const hash = location.hash.slice(1);
  const [route, query] = hash.startsWith("/") ? [hash.split("?")[0], hash.split("?")[1] || ""] : ["", hash];
  const fromHash = new URLSearchParams(query).get("t");
  if (fromHash) {
    sessionStorage.setItem("facet-token", fromHash);
    history.replaceState(null, "", location.pathname + (route ? "#" + route : ""));
    return fromHash;
  }
  return sessionStorage.getItem("facet-token") || "";
})();

const $ = (id) => document.getElementById(id);
const conversation = $("conversation");
const messages = new Map(); // message_id -> element
const chips = new Map(); // tool name -> element of the latest call
const approvals = []; // pending approval requests, oldest first
let busy = false;
let model = "";
let agentStatus = "";

async function api(path, body) {
  const response = await fetch(path, {
    method: body === undefined ? "GET" : "POST",
    headers: { "X-Facet-Token": token, "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const data = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(data.error || response.statusText);
  return data;
}

// el builds an element. Text is always set as text, never parsed as HTML.
function el(tag, attrs = {}, ...children) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(attrs)) {
    if (value === undefined || value === null || value === false) continue;
    if (key === "class") node.className = value;
    else if (key === "text") node.textContent = value;
    else if (key.startsWith("on")) node.addEventListener(key.slice(2), value);
    else node.setAttribute(key, value === true ? "" : value);
  }
  node.append(...children.filter((child) => child !== null && child !== undefined && child !== false));
  return node;
}

// fill replaces node's children, skipping absent ones as el does:
// replaceChildren itself would show a null as the text "null".
function fill(node, ...children) {
  node.replaceChildren(...children.filter((child) => child !== null && child !== undefined && child !== false));
}

// mediaSrc is the address the browser loads a project file from. Video,
// audio and image elements cannot send the token as a header, so it goes in
// the address.
function mediaSrc(url) {
  return url + (url.includes("?") ? "&" : "?") + "t=" + encodeURIComponent(token);
}

function sizeText(bytes) {
  if (!(bytes >= 0)) return "";
  const units = ["bytes", "KB", "MB", "GB"];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  return (unit === 0 ? value : value.toFixed(value < 10 ? 1 : 0)) + " " + units[unit];
}

function when(iso) {
  const date = new Date(iso);
  return isNaN(date) || date.getFullYear() < 2000 ? "" : date.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}

function kindOf(name) {
  const ext = name.toLowerCase().split(".").pop();
  if (["mp4", "webm", "mov"].includes(ext)) return "video";
  if (["mp3", "wav", "aac", "m4a", "ogg"].includes(ext)) return "audio";
  if (["png", "jpg", "jpeg", "webp"].includes(ext)) return "image";
  return "text";
}

// --- Conversation -----------------------------------------------------------

function add(className, text) {
  const node = document.createElement("div");
  node.className = className;
  node.textContent = text;
  conversation.append(node);
  if (!conversation.closest(".view").hidden) node.scrollIntoView({ block: "end" });
  return node;
}

function setBusy(value) {
  busy = value;
  $("stop").disabled = !value;
}

function setStatus(status, detail) {
  agentStatus = status === "ready" ? "ready" : detail || status;
  const pill = $("status");
  pill.textContent = agentStatus;
  pill.className = "pill " + status;
}

function setModel(name) {
  model = name || "";
  $("model").textContent = model ? "Model: " + model.replace(/^app\//, "") : "";
  $("change-model").hidden = !model;
  $("setup").hidden = !!model;
}

function toolLabel(tool) {
  return tool.facet ? tool.facet.replaceAll("_", " ") : tool.name;
}

function showApproval() {
  const dialog = $("approval");
  const next = approvals[0];
  if (!next) {
    if (dialog.open) dialog.close();
    return;
  }
  $("approval-title").textContent = next.tool.paid ? "Approve a paid tool?" : "Approve this tool?";
  $("approval-paid").hidden = !next.tool.paid;
  $("approval-tool").textContent = toolLabel(next.tool);
  $("approval-args").textContent = JSON.stringify(next.args || {}, null, 2);
  dialog.dataset.id = next.id;
  if (!dialog.open) dialog.showModal();
}

setInterval(() => {
  const next = approvals[0];
  if (!next || !$("approval").open) return;
  const seconds = Math.max(0, Math.round((new Date(next.deadline) - Date.now()) / 1000));
  $("approval-countdown").textContent = `(denied automatically in ${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")})`;
}, 1000);

function render(ev) {
  switch (ev.kind) {
    case "status":
      setStatus(ev.status, ev.detail);
      if (ev.status === "failed") add("notice", "The agent stopped: " + (ev.detail || "see its log"));
      break;
    case "user_message":
      add("msg user", ev.text);
      break;
    case "turn_start":
      setBusy(true);
      break;
    case "turn_end":
      setBusy(false);
      if (ev.status === "aborted") add("notice", "Stopped.");
      if (ev.status === "error") add("notice", "The turn ended with an error.");
      break;
    case "message": {
      let node = messages.get(ev.message_id);
      if (!node) {
        node = add("msg" + (ev.thought ? " thought" : ""), "");
        if (ev.message_id) messages.set(ev.message_id, node);
      }
      node.textContent = ev.text;
      if (!conversation.closest(".view").hidden) node.scrollIntoView({ block: "end" });
      break;
    }
    case "message_deleted":
      messages.get(ev.message_id)?.remove();
      messages.delete(ev.message_id);
      break;
    case "tool_call": {
      const chip = add("chip", "Using " + toolLabel(ev.tool));
      if (ev.tool.paid) {
        const badge = document.createElement("span");
        badge.className = "badge";
        badge.textContent = "may charge";
        chip.append(badge);
      }
      chips.set(ev.tool.facet || ev.tool.name, chip);
      break;
    }
    case "progress": {
      const chip = chips.get(ev.tool?.facet || ev.tool?.name);
      if (chip) chip.firstChild.textContent = (ev.text || "Working") + " ";
      break;
    }
    case "approval_request":
      approvals.push(ev.approval);
      showApproval();
      break;
    case "approval_resolved": {
      const i = approvals.findIndex((a) => a.id === ev.approval.id);
      if (i >= 0) approvals.splice(i, 1);
      add("notice", (ev.approval.approved ? "Approved " : "Not approved: ") + toolLabel(ev.approval.tool) +
        (ev.approval.approved ? "" : ev.approval.reason ? " (" + ev.approval.reason + ")" : ""));
      showApproval();
      break;
    }
    case "notice":
      add("notice", ev.text || ev.detail || "");
      break;
  }
}

function connect() {
  const source = new EventSource("/api/events?t=" + encodeURIComponent(token));
  source.onmessage = (e) => render(JSON.parse(e.data));
  source.onerror = () => {
    setStatus("connecting", "reconnecting");
    source.close();
    setTimeout(() => {
      conversation.replaceChildren();
      messages.clear();
      chips.clear();
      approvals.length = 0;
      connect();
    }, 2000);
  };
}

$("composer").addEventListener("submit", async (e) => {
  e.preventDefault();
  const text = $("input").value.trim();
  if (!text) return;
  $("input").value = "";
  try {
    await api("/api/send", { session: SESSION, text });
  } catch (err) {
    add("notice", "Not sent: " + err.message);
    $("input").value = text;
  }
});
$("input").addEventListener("keydown", (e) => {
  if (e.key === "Enter" && !e.shiftKey) {
    e.preventDefault();
    $("composer").requestSubmit();
  }
});
$("stop").addEventListener("click", () => api("/api/stop", { session: SESSION }).catch((err) => add("notice", err.message)));

$("approval-form").addEventListener("submit", (e) => {
  const id = $("approval").dataset.id;
  const approve = e.submitter?.value === "approve";
  api("/api/answer", { id, approve }).catch((err) => add("notice", err.message));
});
$("approval").addEventListener("cancel", (e) => e.preventDefault()); // Escape does not answer

$("setup-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const form = new FormData(e.target);
  $("setup-error").textContent = "";
  e.submitter.disabled = true;
  try {
    const data = await api("/api/model", Object.fromEntries(form));
    setModel(data.model);
    e.target.reset();
  } catch (err) {
    $("setup-error").textContent = err.message;
  } finally {
    e.submitter.disabled = false;
  }
});
$("change-model").addEventListener("click", () => {
  location.hash = "#/";
  $("setup").hidden = false;
});

// --- Screens ----------------------------------------------------------------

const screens = ["conversation", "projects", "project", "readiness"];

// route shows the screen the address names. An address may also carry the
// run's token (#/projects?t=<token>); as at load, it is kept for the tab and
// taken out of the address bar.
function route() {
  const [hash, query = ""] = location.hash.replace(/^#/, "").split("?");
  const fresh = new URLSearchParams(query).get("t");
  if (fresh) {
    token = fresh;
    sessionStorage.setItem("facet-token", fresh);
    history.replaceState(null, "", location.pathname + (hash ? "#" + hash : ""));
  }
  let screen = "conversation";
  let slug = "";
  if (hash === "/projects") screen = "projects";
  else if (hash.startsWith("/projects/")) {
    screen = "project";
    try {
      slug = decodeURIComponent(hash.slice("/projects/".length));
    } catch {
      slug = "";
    }
  } else if (hash === "/readiness") screen = "readiness";
  for (const name of screens) $("view-" + name).hidden = name !== screen;
  $("footer").hidden = screen !== "conversation";
  const tab = screen === "project" ? "projects" : screen;
  document.querySelectorAll("nav a").forEach((a) => {
    if (a.dataset.view === tab) a.setAttribute("aria-current", "page");
    else a.removeAttribute("aria-current");
  });
  if (screen === "projects") loadProjects();
  if (screen === "project") loadProject(slug);
  if (screen === "readiness") loadReadiness();
}
window.addEventListener("hashchange", route);

// sheet shows a result in the shared dialog.
function sheet(title, ...body) {
  $("sheet-title").textContent = title;
  $("sheet-body").replaceChildren(...body);
  if (!$("sheet").open) $("sheet").showModal();
}

// --- Projects ---------------------------------------------------------------

const STAGES = [["brief", "Brief"], ["script", "Script"], ["voiceover", "Voice"], ["composition", "Edit"], ["review", "Review"], ["master", "Final"]];

function stageChips(stages) {
  return STAGES.map(([key, label]) => el("span", { class: "stage" + (stages?.[key] ? " done" : ""), text: label, title: stages?.[key] ? label + ": on disk" : label + ": nothing yet" }));
}

function projectHref(slug) {
  return "#/projects/" + encodeURIComponent(slug);
}

async function loadProjects() {
  $("projects-error").textContent = "";
  try {
    const data = await api("/api/projects");
    $("productions-root").textContent = data.productions_root || "the App's productions folder";
    const list = $("project-list");
    list.replaceChildren(...data.projects.map((p) => {
      const thumb = p.thumbnail_url
        ? el("img", { class: "thumb", src: mediaSrc(p.thumbnail_url), alt: "", loading: "lazy" })
        : el("div", { class: "thumb blank", text: p.name.slice(0, 1).toUpperCase() });
      return el("a", { class: "card", href: projectHref(p.slug) },
        thumb,
        el("strong", { text: p.name }),
        el("div", { class: "stages" }, ...stageChips(p.stages)),
        el("span", { class: "muted small", text: (p.in_workspace ? "" : "Opened folder · ") + "Changed " + when(p.last_modified) }));
    }));
    $("projects-empty").hidden = data.projects.length > 0;
  } catch (err) {
    $("projects-error").textContent = err.message;
  }
}

async function submitForm(form, action) {
  const error = form.querySelector(".error");
  const button = form.querySelector("button[type=submit]");
  error.textContent = "";
  button.disabled = true;
  try {
    await action(new FormData(form));
  } catch (err) {
    error.textContent = err.message;
  } finally {
    button.disabled = false;
  }
}

$("create-form").addEventListener("submit", (e) => {
  e.preventDefault();
  submitForm(e.target, async (form) => {
    const data = await api("/api/projects", { name: form.get("name") });
    e.target.reset();
    location.hash = projectHref(data.project.id);
  });
});

$("open-form").addEventListener("submit", (e) => {
  e.preventDefault();
  submitForm(e.target, async (form) => {
    const data = await api("/api/projects/open", { path: form.get("path") });
    e.target.reset();
    location.hash = projectHref(data.project.id);
  });
});
$("open-form").elements.path.placeholder = /Windows/.test(navigator.userAgent) ? "C:\\Users\\user\\Videos\\My film" : "/Users/you/Videos/My film";

// --- One project ------------------------------------------------------------

let shown = null; // the project on screen: { slug, data }

async function loadProject(slug, keepScroll) {
  const scroll = window.scrollY;
  if (!shown || shown.slug !== slug) {
    shown = { slug };
    $("p-body").hidden = true;
  }
  $("p-crumb").textContent = slug;
  $("p-error").textContent = "";
  try {
    const data = await api("/api/projects/" + encodeURIComponent(slug));
    if (shown.slug !== slug) return; // the person moved on
    shown.data = data;
    renderProject(data);
    $("p-body").hidden = false;
    if (keepScroll) window.scrollTo(0, scroll);
  } catch (err) {
    $("p-error").textContent = err.message;
  }
}

function renderProject(data) {
  const p = data.project;
  $("p-crumb").textContent = p.name;
  $("p-name").textContent = p.name;
  $("p-path").textContent = p.path;
  $("p-stages").replaceChildren(...stageChips(p.stages));
  $("p-outside").hidden = data.in_workspace;

  const video = p.video_url || p.preview_video_url;
  $("p-player").hidden = !video;
  if (video) {
    const src = mediaSrc(video);
    if ($("p-video").getAttribute("src") !== src) $("p-video").setAttribute("src", src);
    if (p.thumbnail_url) $("p-video").setAttribute("poster", mediaSrc(p.thumbnail_url));
    else $("p-video").removeAttribute("poster");
    $("p-video-caption").textContent = (p.video_url ? "Final render: " : "Latest preview: ") + (p.video_path || p.preview_video_path).split("/").pop();
  } else {
    $("p-video").removeAttribute("src");
  }

  $("p-brief-block").hidden = !p.brief;
  $("p-brief").textContent = p.brief || "";
  renderScript(p);
  renderRenders(data);
  renderMedia(data);
  renderReview(data);
  renderFiles(data);
}

function renderScript(p) {
  const beats = p.beats || [];
  $("p-script-block").hidden = !p.script && beats.length === 0;
  $("p-beats").hidden = beats.length === 0;
  $("p-beats").tBodies[0].replaceChildren(...beats.map((b) => el("tr", {},
    el("td", { text: b.index || "" }),
    el("td", { text: b.time_range || "" }),
    el("td", { text: b.title || "" }),
    el("td", { text: b.narration || b.visual || "" }))));
  $("p-script-details").hidden = !p.script;
  $("p-script-details").open = beats.length === 0;
  $("p-script").textContent = p.script || "";
}

function play(file) {
  $("p-player").hidden = false;
  $("p-video").setAttribute("src", mediaSrc(file.url));
  $("p-video").removeAttribute("poster");
  $("p-video-caption").textContent = "Playing: " + file.name;
  $("p-player").scrollIntoView({ block: "center" });
  $("p-video").play().catch(() => {});
}

function renderRenders(data) {
  $("p-renders").replaceChildren(...data.renders.map((file) => el("li", { class: "row" },
    el("span", { class: "grow" }, el("strong", { text: file.name }), " ", el("span", { class: "muted small", text: sizeText(file.size) + " · " + when(file.last_modified) })),
    el("button", { type: "button", class: "small secondary", text: "Play", onclick: () => play(file) }),
    el("button", { type: "button", class: "small secondary", text: "Inspect", onclick: () => inspect(file) }),
    el("button", { type: "button", class: "small", text: "Review", onclick: () => {
      $("review-render").value = file.relative_path;
      $("review-render").dispatchEvent(new Event("change"));
      $("review-block").scrollIntoView({ block: "start" });
    } }))));
  $("p-renders-empty").hidden = data.renders.length > 0;
}

function preview(file) {
  const src = mediaSrc(file.url);
  switch (kindOf(file.name)) {
    case "image":
      return el("img", { src, alt: file.name, loading: "lazy" });
    case "video":
      return el("video", { src, controls: true, preload: "metadata" });
    case "audio":
      return el("audio", { src, controls: true, preload: "none" });
  }
  return null;
}

function renderMedia(data) {
  const p = data.project;
  const files = [...(p.assets || []), ...(p.narration || [])];
  $("p-media").replaceChildren(...files.map((file) => el("div", { class: "tile" },
    preview(file),
    el("span", { class: "name", text: file.name, title: file.relative_path }),
    el("span", { class: "row-end" },
      el("span", { class: "muted small", text: sizeText(file.size) }),
      el("button", { type: "button", class: "small secondary", text: "Inspect", onclick: () => inspect(file) })))));
  $("p-media-empty").hidden = files.length > 0;
  $("import-hint").textContent = "Imported files go in the project's assets folder, under a safe name. Video, audio and images up to " + sizeText(data.max_upload) + " each.";
}

function renderFiles(data) {
  const files = data.files;
  $("p-files-block").hidden = files.length === 0;
  $("p-files").replaceChildren(...files.map((file) => {
    const kind = kindOf(file.name);
    return el("li", { class: "row" },
      kind === "image" ? el("img", { class: "mini", src: mediaSrc(file.url), alt: "", loading: "lazy" }) : null,
      el("span", { class: "grow" }, el("strong", { text: file.name }), " ", el("span", { class: "muted small", text: file.relative_path.split("/").slice(2).join("/") + " · " + sizeText(file.size) })),
      kind === "text" ? el("button", { type: "button", class: "small secondary", text: "View", onclick: () => viewText(file) }) : null,
      kind === "video" || kind === "audio" || kind === "image" ? el("button", { type: "button", class: "small secondary", text: "Inspect", onclick: () => inspect(file) }) : null);
  }));
}

// viewText shows a document from the project; fetched with the token as a
// header, so its address never needs it.
async function viewText(file) {
  sheet(file.name, el("p", { class: "muted", text: "Loading…" }));
  try {
    const response = await fetch(file.url, { headers: { "X-Facet-Token": token } });
    if (!response.ok) throw new Error((await response.json().catch(() => ({}))).error || response.statusText);
    sheet(file.name, el("pre", { class: "doc tall", text: await response.text() }));
  } catch (err) {
    sheet(file.name, el("p", { class: "error", text: err.message }));
  }
}

// --- Media import -----------------------------------------------------------

function upload(slug, file, ifExists, onProgress) {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    const query = new URLSearchParams({ name: file.name });
    if (ifExists) query.set("if_exists", ifExists);
    xhr.open("POST", "/api/projects/" + encodeURIComponent(slug) + "/media?" + query);
    xhr.setRequestHeader("X-Facet-Token", token);
    xhr.setRequestHeader("Content-Type", "application/octet-stream");
    xhr.upload.onprogress = (e) => e.lengthComputable && onProgress(e.loaded / e.total);
    xhr.onload = () => {
      let data = {};
      try {
        data = JSON.parse(xhr.responseText);
      } catch {}
      if (xhr.status >= 200 && xhr.status < 300) return resolve(data);
      const err = new Error(data.error || xhr.statusText || "the import failed");
      err.status = xhr.status;
      reject(err);
    };
    xhr.onerror = () => reject(new Error("the file did not reach the App"));
    xhr.send(file);
  });
}

// askConflict asks what to do with a file whose name the project has:
// "replace", "keep" or "skip".
function askConflict(name) {
  return new Promise((resolve) => {
    const dialog = $("conflict");
    $("conflict-name").textContent = name;
    dialog.returnValue = "skip";
    dialog.addEventListener("close", () => resolve(dialog.returnValue || "skip"), { once: true });
    dialog.showModal();
  });
}

$("import-files").addEventListener("change", async (e) => {
  const files = [...e.target.files];
  e.target.value = "";
  if (!shown?.data || files.length === 0) return;
  const slug = shown.slug;
  const limit = shown.data.max_upload;
  const progress = $("import-progress");
  for (const file of files) {
    const status = el("span", { class: "muted small", text: "waiting" });
    const row = el("li", { class: "row" }, el("span", { class: "grow", text: file.name }), status);
    progress.append(row);
    if (file.size > limit) {
      status.textContent = "larger than the " + sizeText(limit) + " limit; not imported";
      status.className = "error small";
      continue;
    }
    let choice = "";
    for (;;) {
      try {
        const data = await upload(slug, file, choice, (share) => (status.textContent = Math.round(share * 100) + "%"));
        status.textContent = (data.imported.replaced ? "replaced " : "imported as ") + data.imported.name;
        break;
      } catch (err) {
        if (err.status === 409 && !choice) {
          choice = await askConflict(file.name);
          if (choice !== "skip") continue;
          status.textContent = "skipped";
        } else {
          status.textContent = err.message;
          status.className = "error small";
        }
        break;
      }
    }
  }
  // A long import may end after the person has opened another project;
  // only the project it was for is reloaded.
  if (shown?.slug === slug) await loadProject(slug, true);
  setTimeout(() => progress.replaceChildren(), 8000);
});

// --- Inspection -------------------------------------------------------------

function factsTable(rows) {
  return el("table", { class: "facts" }, el("tbody", {}, ...rows.filter(([, value]) => value).map(([label, value]) => el("tr", {}, el("th", { text: label }), el("td", { text: value })))));
}

function probeFacts(result, warnings) {
  const format = result.format || {};
  const video = result.video;
  const audio = (result.audio_streams || [])[0];
  const fps = video?.fps ? Math.round(video.fps * 100) / 100 + " fps" : "";
  return [
    factsTable([
      ["Length", format.duration > 0 ? format.duration.toFixed(2) + " s" : ""],
      ["Size", sizeText(format.size)],
      ["Container", format.format_long_name || format.format_name],
      ["Video", video ? [video.codec, video.width && video.width + " × " + video.height, fps, video.pixel_format].filter(Boolean).join(", ") : "none"],
      ["Audio", audio ? [audio.codec, audio.sample_rate && audio.sample_rate + " Hz", audio.channel_layout || (audio.channels && audio.channels + " channels")].filter(Boolean).join(", ") : "none"],
      ["SHA-256", result.sha256],
    ]),
    (warnings || []).length ? el("p", { class: "muted small", text: "Notes: " + warnings.join("; ") }) : null,
    el("details", {}, el("summary", { text: "Everything media_probe reported" }), el("pre", { class: "doc tall", text: JSON.stringify(result, null, 2) })),
  ].filter(Boolean);
}

async function inspect(file) {
  const title = "Inspect " + file.name;
  sheet(title, el("p", { class: "muted", text: "Inspecting with media_probe…" }));
  try {
    const envelope = await api("/api/probe", { ref: file.relative_path });
    if (!envelope.ok) sheet(title, el("p", { class: "error", text: envelope.error?.message || "media_probe could not inspect the file" }));
    else sheet(title, ...probeFacts(envelope.result, envelope.warnings));
  } catch (err) {
    sheet(title, el("p", { class: "error", text: err.message }));
  }
}

// --- Review and decision ----------------------------------------------------

const GATES = { profile: "Size and frame rate", duration: "Length", video_codec: "Video codec", pixel_format: "Pixel format", audio: "Sound", content: "Picture content" };

function verdict(status) {
  const words = { pass: "Passed", warn: "Passed with notes", fail: "Failed", assumed: "Not checked", unknown: "Unknown" };
  return el("span", { class: "verdict " + (status || "unknown"), text: words[status] || status || "Unknown" });
}

function frameStrip(frames, caption) {
  return el("div", { class: "frames" }, ...frames.map((frame) => el("figure", {},
    el("img", { src: mediaSrc(frame.url), alt: caption(frame), loading: "lazy" }),
    el("figcaption", { text: caption(frame) }))));
}

function renderReport(data) {
  const report = data.project.review_report;
  const kept = [...(data.project.review_frames || []), ...(data.project.qa_frames || [])];
  const keptFrames = kept.length
    ? el("details", {}, el("summary", { text: "Frames kept for review (" + kept.length + ")" }), frameStrip(kept, (frame) => frame.name))
    : null;
  const box = $("p-report");
  if (!report) {
    fill(box, el("p", { class: "muted small", text: "No technical review yet." }), keptFrames);
    return;
  }
  const gates = Array.isArray(report.gates) ? report.gates.filter((gate) => gate && typeof gate === "object") : [];
  const samples = data.review?.samples || [];
  fill(box,
    el("p", {}, el("strong", { text: "Latest technical review " }), verdict(report.status),
      data.review?.file ? el("span", { class: "muted small", text: " of " + data.review.file }) : null),
    gates.length ? el("table", { class: "gates" },
      el("thead", {}, el("tr", {}, el("th", { text: "Check" }), el("th", { text: "Result" }), el("th", { text: "Detail" }))),
      el("tbody", {}, ...gates.map((gate) => el("tr", {},
        el("td", { text: GATES[gate.name] || gate.name }),
        el("td", {}, verdict(gate.status)),
        el("td", { class: "muted small", text: gate.message || "" }))))) : null,
    samples.length ? frameStrip(samples, (sample) => sample.timestamp.toFixed(1) + " s") : null,
    keptFrames);
}

function renderDecision(data) {
  const decision = data.project.decision;
  const box = $("p-decision");
  if (!decision) {
    box.replaceChildren(el("p", { class: "muted small", text: "No decision recorded yet." }));
    return;
  }
  // Only a record this App sealed is the person's decision: the agent can
  // write files in a project, including a decision record.
  if (!decision.sealed) {
    box.replaceChildren(el("p", { class: "error small", text: "This project has a decision file (" + decision.decision + ", " +
      decision.artifact + ") that this App did not record: another program, or the agent, wrote it. It is not your decision; record your own below." }));
    return;
  }
  box.replaceChildren(el("div", { class: "decision " + decision.decision },
    el("p", {}, el("strong", { text: decision.decision === "accepted" ? "Accepted" : "Rejected" }),
      el("span", { class: "muted small", text: " · " + decision.artifact + " · " + when(decision.decided_at) })),
    decision.note ? el("p", { text: "“" + decision.note + "”" }) : null,
    el("p", { class: "muted small", text: "SHA-256 " + decision.sha256.slice(0, 16) + "… · " + sizeText(decision.size) + " · " +
      (decision.current ? "the file is unchanged since." : "the file has changed since, or is gone: this decision is about other bytes.") })));
}

function renderReview(data) {
  const select = $("review-render");
  const previous = select.value;
  select.replaceChildren(...data.renders.map((file) => el("option", { value: file.relative_path, text: file.name })));
  const preferred = [previous, data.review?.file && data.ref + "/" + data.review.file, data.project.video_path].find((ref) => ref && data.renders.some((file) => file.relative_path === ref));
  if (preferred) select.value = preferred;
  $("review-none").hidden = data.renders.length > 0;
  $("review-tools").hidden = data.renders.length === 0;
  renderReport(data);
  renderDecision(data);
}

function applyPreset(form) {
  const [width, height, fps] = (form.elements.preset.value || "").split("x");
  if (!width) return;
  form.elements.width.value = width;
  form.elements.height.value = height;
  form.elements.fps.value = fps;
}
applyPreset($("review-form"));
$("review-form").elements.preset.addEventListener("change", () => applyPreset($("review-form")));
for (const name of ["width", "height", "fps"]) {
  $("review-form").elements[name].addEventListener("input", () => ($("review-form").elements.preset.value = ""));
}

$("review-form").addEventListener("submit", (e) => {
  e.preventDefault();
  const button = e.submitter;
  submitForm(e.target, async (form) => {
    const body = { ref: $("review-render").value, audio: form.get("audio") === "on" };
    for (const key of ["width", "height", "audio_sample_rate", "audio_channels"]) if (form.get(key)) body[key] = parseInt(form.get(key), 10);
    for (const key of ["fps", "duration", "duration_tolerance"]) if (form.get(key)) body[key] = parseFloat(form.get(key));
    for (const key of ["video_codec", "pixel_format", "audio_codec"]) if (form.get(key).trim()) body[key] = form.get(key).trim();
    button.textContent = "Reviewing…";
    try {
      const result = await api("/api/review", body);
      await loadProject(shown.slug, true);
      if (!result.envelope.ok) throw new Error("The review could not finish: " + (result.envelope.error?.message || "output_review failed"));
    } finally {
      button.textContent = "Run technical review";
    }
  });
});

$("decision-form").addEventListener("submit", (e) => {
  e.preventDefault();
  submitForm(e.target, async (form) => {
    const decision = form.get("decision");
    const note = (form.get("note") || "").trim();
    if (decision === "reject" && !note) throw new Error("Say what must change before you reject the render.");
    await api("/api/decision", { ref: $("review-render").value, decision, note });
    e.target.reset();
    await loadProject(shown.slug, true);
  });
});

// --- Readiness --------------------------------------------------------------

function need(dep) {
  const names = dep.alternatives?.length ? dep.alternatives.join(" or ") : dep.name;
  switch (dep.type) {
    case "env":
      return "the key " + names;
    case "binary":
      return names + " installed";
    case "runtime":
      return "the " + names + " runtime";
  }
  return names;
}

function toolRow(tool) {
  const status = tool.configured
    ? tool.unverified.length ? "Ready; " + tool.unverified.join(", ") + " is set but not verified until the provider answers" : "Ready"
    : "Needs " + tool.missing.map(need).join(", and ");
  return el("li", { class: "row" },
    el("span", { class: "grow" },
      el("code", { text: tool.name }), " ", el("span", { class: "muted small", text: tool.capability }),
      el("span", { class: "status small" + (tool.configured ? "" : " needs"), text: status })),
    tool.may_charge ? el("span", { class: "tag paid", text: "may charge" }) : null,
    tool.network ? el("span", { class: "tag", text: "online" }) : null,
    tool.direct ? el("span", { class: "tag", text: "runs in this window", title: "Read-only or local, and free: this window may run it itself" }) : null);
}

async function loadReadiness() {
  $("r-error").textContent = "";
  try {
    const data = await api("/api/readiness");
    await stateLoaded;
    const ready = data.tools.filter((t) => t.configured);
    const missing = data.tools.filter((t) => !t.configured);
    fill($("r-summary"),
      el("p", {}, el("strong", { text: "Facet Toolkit " + (data.version || "(version unknown)") }),
        el("span", { class: "muted", text: " · " + ready.length + " of " + data.tools.length + " tools ready on this computer" })),
      el("p", { class: "muted small", text: model
        ? "The agent uses " + model.replace(/^app\//, "") + " (" + agentStatus + ")."
        : "The agent has no model yet: choose one in Conversation. Projects and Readiness work without it." }),
      data.version_error ? el("p", { class: "error small", text: data.version_error }) : null);
    $("r-missing-title").hidden = missing.length === 0;
    $("r-missing").replaceChildren(...missing.map(toolRow));
    $("r-ready").replaceChildren(...ready.map(toolRow));
  } catch (err) {
    $("r-error").textContent = err.message;
  }
}

const stateLoaded = api("/api/state").then((s) => {
  setModel(s.model);
  setStatus(s.status || "starting", s.detail);
}).catch((err) => setStatus("failed", err.message));
connect();
route();
