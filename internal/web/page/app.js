// The web cockpit: a remote control for the sessions cicoria shares.
//
// It does two jobs first: it says who needs you and what they ask, and it
// shows what an agent is doing now, its screen live and what it said last.
// An agent's question is answered with one of its own choices, in words.
// Nothing is done on the user's behalf, and each thing done gets a receipt.
// The page is drawn once and then changed only where the snapshot changed,
// so that focus, scroll and a screen reader's place stay where they were.
"use strict";

const $ = (sel, root = document) => root.querySelector(sel);
const main = $("#main");
const home = $("#home");

// The token comes in the link. It is kept for this tab, which is scoped to
// this port as a cookie would not be, and leaves the address bar.
const token = (() => {
  const t = new URLSearchParams(location.search).get("t");
  try {
    if (t) sessionStorage.setItem("token", t);
    return t || sessionStorage.getItem("token") || "";
  } catch {
    return t || "";
  }
})();
if (location.search) history.replaceState(null, "", "/" + location.hash);

let snap = { workspace: "", sessions: [] };
let loaded = false;
let source = null;
let watched = null;
let view = null;
const seen = new Map(); // id → the status last seen, to tell what changed
const receipts = new Map(); // id → what the user did from this page

// ---- Small tools.

function el(tag, attrs = {}, ...children) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k === "class") e.className = v;
    else if (k === "text") e.textContent = v;
    else if (k.startsWith("on")) e.addEventListener(k.slice(2), v);
    else if (v !== false && v != null) e.setAttribute(k, v === true ? "" : v);
  }
  for (const c of children.flat()) if (c != null && c !== false) e.append(c);
  return e;
}

// set changes a node's text only when it differs.
function set(node, text) {
  if (node.textContent !== text) node.textContent = text;
}

// sync makes a list's children match items by key, keeping the nodes that
// stay: create makes a new one, paint brings one up to date.
function sync(list, items, key, create, paint) {
  const old = new Map([...list.children].map((n) => [n.dataset.key, n]));
  let at = list.firstElementChild;
  for (const item of items) {
    const k = String(key(item));
    let node = old.get(k);
    if (node) old.delete(k);
    else {
      node = create(item);
      node.dataset.key = k;
    }
    paint(node, item);
    if (node !== at) list.insertBefore(node, at);
    else at = at.nextElementSibling;
  }
  for (const n of old.values()) n.remove();
}

// announce says something to a screen reader. Each thing said is a line
// of its own, so that one does not cut off another.
function announce(text) {
  const a = $("#announce");
  const p = el("p", { text });
  a.append(p);
  setTimeout(() => p.remove(), 15000);
  while (a.children.length > 6) a.firstElementChild.remove();
}

function api(path, init = {}) {
  return fetch(path, { ...init, headers: { ...init.headers, Authorization: `Bearer ${token}` } });
}

function short(text, n = 70) {
  const one = text.replace(/\s+/g, " ").trim();
  return one.length > n ? one.slice(0, n - 1) + "…" : one;
}

// span tells a length of time in its largest unit.
function span(iso) {
  const t = Date.parse(iso);
  if (!t || t < 0) return "";
  const s = Math.max(0, (Date.now() - t) / 1000);
  if (s < 60) return "a moment";
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  if (s < 172800) return `${Math.floor(s / 3600)}h`;
  return `${Math.floor(s / 86400)}d`;
}

// ---- What a session is doing, in words.

function group(s) {
  switch (s.status) {
    case "needs input": return "needs";
    case "working": case "starting": return "working";
    case "exited": return "exited";
    default: return "idle";
  }
}

function mark(s) {
  return s.status === "working" && s.stalled ? "stalled" : group(s);
}

function state(s) {
  const t = span(s.since);
  switch (s.status) {
    case "needs input": return `Needs you, waiting ${t}`;
    case "working": return s.stalled ? `Working, but quiet for ${t}` : `Working for ${t}`;
    case "starting": return "Starting";
    case "exited": return `Exited ${t} ago`;
    default: return `Finished its turn ${t} ago`;
  }
}

// asks is the one line that says what a session waiting for you wants.
function asks(s) {
  if (s.question) return s.question;
  if (s.prompt) return s.prompt;
  if (s.tool) return s.tool.detail ? `${s.tool.name}: ${short(s.tool.detail)}` : `Wants to use ${s.tool.name}`;
  return "Waiting for you: open it to see its screen.";
}

// doing says what an agent at work is busy with, when it tells.
function doing(s) {
  if (s.status !== "working" || !s.tool) return "";
  return s.tool.detail ? `Using ${s.tool.name}: ${s.tool.detail}` : `Using ${s.tool.name}`;
}

const GROUPS = [["needs", "Needs you"], ["working", "Working"], ["idle", "Finished"], ["exited", "Exited"]];

// queued says what waits to be sent, and why it does not go.
function queued(s) {
  if (!s.queue.length) return "";
  return `${s.queue.length} queued${s.paused ? ", held back" : ""}`;
}

// plain drops what only draws, frames, blocks, spinners and the marks
// agents put before a line, so that the words of the screen read on their
// own.
function plain(text) {
  return text
    .replace(/[─-▟⠀-⣿⌀-⏿■-◿✀-➿]/g, " ")
    .split("\n").map((l) => l.replace(/\s+$/, "")).join("\n")
    .replace(/\n{3,}/g, "\n\n")
    .trim();
}

// prose sets an answer as paragraphs, with what is between backticks as
// code.
function prose(text) {
  return text.trim().split(/\n{2,}/).filter(Boolean).map((para) =>
    el("p", {}, para.split(/(`[^`\n]+`)/).map((part) =>
      part.length > 2 && part.startsWith("`") && part.endsWith("`") ? el("code", { text: part.slice(1, -1) }) : part)));
}

// ---- The stream.

function setOnline(on) {
  document.body.classList.toggle("online", on);
  set($("#conn"), on ? "live" : "offline");
  $("#offline").hidden = on || !loaded;
}

// connect streams the snapshots, with the screen of the session watched.
function connect(watch) {
  if (source && watched === watch && source.readyState !== EventSource.CLOSED) return;
  if (source) source.close();
  watched = watch;
  source = new EventSource(`/events?t=${encodeURIComponent(token)}` + (watch ? `&watch=${encodeURIComponent(watch)}` : ""));
  source.onopen = () => setOnline(true);
  source.onerror = () => {
    setOnline(false);
    // A stream that is refused is not tried again: the token is not taken.
    if (source.readyState === EventSource.CLOSED) {
      view = null;
      document.body.dataset.route = "gone";
      $("#offline").hidden = true;
      home.replaceChildren();
      main.replaceChildren(el("div", { class: "notice" },
        el("h1", { text: "This link no longer works" }),
        el("p", { text: "cicoria makes a new link each time the web cockpit starts. Open the one it shows now." })));
    }
  };
  source.onmessage = (e) => {
    snap = JSON.parse(e.data);
    loaded = true;
    $("#offline").hidden = document.body.classList.contains("online");
    notice();
    update();
  };
}

// notice says which statuses changed, and what a session asks when it
// starts to need you.
function notice() {
  const said = [];
  for (const s of snap.sessions) {
    const was = seen.get(s.id);
    if (was && was !== s.status) {
      said.push(s.status === "needs input" ? `${s.name} needs you: ${asks(s)}`
        : `${s.name}: ${s.status === "idle" ? "finished its turn" : s.status}`);
    }
    seen.set(s.id, s.status);
  }
  for (const line of said) announce(line);
}

// title names the session open and counts the sessions that need you.
function title() {
  const needs = snap.sessions.filter((s) => group(s) === "needs").length;
  const open = snap.sessions.find((s) => s.id === route()?.id);
  document.title = (needs ? `(${needs}) ` : "") + (open ? `${open.name} – cicoria` : "cicoria");
}

// act asks something of a session; what came of it becomes a receipt.
async function act(s, what, body, done) {
  let r;
  try {
    r = await api(`/api/sessions/${encodeURIComponent(s.id)}/${what}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
  } catch {
    receipt(s, "Not done: cicoria cannot be reached.", true);
    return false;
  }
  if (!r.ok) {
    const why = (await r.text()).trim() || `error ${r.status}`;
    receipt(s, `Not done: ${why}.`, true);
    return false;
  }
  receipt(s, done);
  return true;
}

function receipt(s, text, failed = false) {
  const list = receipts.get(s.id) || [];
  list.unshift({ text, failed, at: new Date(), n: `${Date.now()}-${list.length}` });
  receipts.set(s.id, list.slice(0, 3));
  announce(text);
  update();
}

// ---- Routes: the list, a session, its changes.

function route() {
  const m = location.hash.match(/^#\/s\/([^/]+)(\/changes)?$/);
  return m ? { id: decodeURIComponent(m[1]), changes: !!m[2] } : null;
}

function render() {
  const r = route();
  document.body.dataset.route = r ? "session" : "home";
  view = !r ? emptyView() : r.changes ? changesView(r.id) : sessionView(r.id);
  main.replaceChildren(view.root);
  // The screen of the session open comes with the snapshots.
  connect(r && !r.changes ? r.id : null);
  update();
  if (r) {
    window.scrollTo(0, 0);
    // A screen reader lands on the name of what was opened.
    (view.focus || main).focus({ preventScroll: true });
  }
}

function update() {
  if (!loaded) return;
  // A wide screen has the list beside the session: rather than an empty
  // pane, it opens the session that matters most.
  if (!route() && wide.matches) {
    const s = first(snap.sessions);
    if (s) {
      location.replace(`#/s/${encodeURIComponent(s.id)}`);
      return;
    }
  }
  title();
  list.update();
  view?.update();
}

const wide = window.matchMedia("(min-width: 900px)");
wide.addEventListener("change", update);

// first is the session to look at first: the one that has waited for you
// longest, or else the one whose state changed last.
function first(list) {
  const by = (a, b) => Date.parse(a.since) - Date.parse(b.since);
  const needs = list.filter((s) => group(s) === "needs").sort(by);
  return needs[0] || [...list].sort(by).at(-1);
}

// ---- The list, by what each session needs of you.

const list = (() => {
  const empty = el("p", { class: "empty" },
    "No session is shared. In cicoria, open the web cockpit with ctrl+a v and press space on a session.");
  const groups = GROUPS.map(([k, label]) => {
    const ul = el("ul", { class: "sessions" });
    const count = el("span", { class: "count" });
    const section = el("section", { class: `group g-${k}` }, el("h2", {}, label, " ", count), ul);
    return { k, ul, count, section };
  });
  home.append(empty, ...groups.map((g) => g.section));

  return {
    update() {
      set($("#workspace"), snap.workspace);
      empty.hidden = snap.sessions.length > 0;
      const open = route()?.id;
      for (const g of groups) {
        let items = snap.sessions.filter((s) => group(s) === g.k);
        // Who has waited longest comes first.
        if (g.k === "needs") items = [...items].sort((a, b) => Date.parse(a.since) - Date.parse(b.since));
        g.section.hidden = !items.length;
        set(g.count, String(items.length));
        sync(g.ul, items, (s) => s.id, row, (li, s) => li.paint(s, open));
      }
    },
  };
})();

function row() {
  const badge = el("span", { class: "badge", "aria-hidden": "true" });
  const name = el("h3", { class: "name" });
  const line = el("p", { class: "line" });
  const where = el("p", { class: "where" });
  const what = el("p", { class: "what" });
  const link = el("a", {}, badge, el("span", { class: "grow" }, name, line, what, where));
  const li = el("li", {}, link);
  li.paint = (s, open) => {
    link.href = `#/s/${encodeURIComponent(s.id)}`;
    link.className = group(s);
    if (s.id === open) link.setAttribute("aria-current", "page");
    else link.removeAttribute("aria-current");
    badge.className = `badge ${mark(s)}`;
    set(name, s.name);
    set(line, state(s));
    const bits = [s.agent, s.worktree ? `${s.project}, worktree ${s.worktree}` : s.project, queued(s)];
    if (s.touched) bits.push(`${s.touched} file${s.touched === 1 ? "" : "s"} touched`);
    if (!s.control) bits.push("watch only");
    set(where, bits.filter(Boolean).join(", "));
    const text = group(s) === "needs" ? asks(s) : doing(s);
    what.hidden = !text;
    set(what, short(text, 120));
  };
  return li;
}

function emptyView() {
  const p = el("p", { class: "empty" });
  return {
    root: el("div", { class: "pick" }, p),
    update() {
      set(p, snap.sessions.length ? "Open a session to see what it is doing and answer it." : "");
    },
  };
}

// ---- One session: what it asks, what it is doing, what it said.

// wantsWords tells the choice after which the agent waits for you to say
// what to do instead.
const wantsWords = (label) => /\btell\b/i.test(label);

function sessionView(id) {
  let s = null;

  // Who it is and where it stands, always in sight.
  const name = el("h1", { class: "name", tabindex: "-1" });
  const where = el("p", { class: "where" });
  const badge = el("span", { class: "badge", "aria-hidden": "true" });
  const stateText = el("span");
  // How much it changed, near the top: on a wide screen it goes down to
  // the changes, on a phone to their page.
  const toChanges = el("a", { class: "to-changes" });
  toChanges.onclick = (e) => {
    if (!wide.matches) return;
    e.preventDefault();
    changes.root.scrollIntoView({ behavior: matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth", block: "start" });
    changes.focus();
  };
  const head = el("header", { class: "head" },
    el("a", { class: "back", href: "#" }, el("span", { "aria-hidden": "true", text: "‹" }), el("span", { class: "sr-only", text: "All sessions" })),
    el("div", { class: "grow" }, name, where, el("p", { class: "state" }, badge, stateText, toChanges)));

  // What it asks: the question, what it is about, and its choices.
  const askText = el("p", { class: "ask-text" });
  const toolName = el("p", { class: "tool-name" });
  const toolDetail = el("pre", { class: "tool-detail" });
  const tool = el("div", { class: "tool" }, toolName, toolDetail);
  const legend = el("legend", { class: "prompt" });
  const options = el("div", { class: "options" });
  const reason = el("textarea", { id: `why-${id}`, rows: 2 });
  const reasonBox = el("div", { class: "reason", hidden: true },
    el("label", { for: `why-${id}`, text: "What should it do instead? It gets this once it has your answer." }), reason);
  const answer = el("button", { type: "submit", class: "primary" });
  const choices = el("form", { class: "choices" }, el("fieldset", {}, legend, options), reasonBox, answer);
  const noMenu = el("p", { class: "note" });
  const ask = el("section", { class: "ask", "aria-labelledby": `ask-${id}` },
    el("h2", { id: `ask-${id}`, text: "It asks" }), askText, tool, choices, noMenu);
  let picked = null;
  let menuKey = "";
  choices.onchange = () => {
    const r = choices.querySelector("input:checked");
    picked = r ? { index: +r.value, label: r.dataset.label } : null;
    paintAnswer();
  };
  reason.oninput = () => paintAnswer();
  function paintAnswer() {
    const words = !!picked && wantsWords(picked.label);
    reasonBox.hidden = !words;
    const told = words && reason.value.trim();
    answer.disabled = !picked;
    set(answer, !picked ? "Pick an answer"
      : told ? `Answer “${short(picked.label.split(",")[0], 30)}” and tell it`
      : `Answer “${short(picked.label, 40)}”`);
    answer.setAttribute("aria-label", picked ? `Answer ${picked.label}${told ? ", then tell it what to do" : ""}, to ${s?.name}` : "Pick an answer first");
  }
  choices.onsubmit = async (e) => {
    e.preventDefault();
    if (!picked || !s) return;
    const p = picked;
    const words = wantsWords(p.label) ? reason.value.trim() : "";
    answer.disabled = true;
    const ok = await act(s, "choose", { index: p.index, text: p.label }, `Answered ${s.name}: “${short(p.label, 60)}”.`);
    if (ok) {
      picked = null;
      reason.value = "";
      for (const r of choices.querySelectorAll("input")) r.checked = false;
      if (words) tellAfter(words);
    }
    paintAnswer();
  };
  // tellAfter sends what to do instead once the menu has left the screen;
  // if it does not go, it is put back in the message box.
  async function tellAfter(msg) {
    for (let i = 0; i < 20 && current()?.choices?.length; i++) await new Promise((r) => setTimeout(r, 250));
    const now = current();
    if (!now || !(await act(now, "send", { text: msg }, `Told ${now.name}: “${short(msg, 60)}”.`))) {
      if (!text.value.trim()) text.value = msg;
    }
  }
  const current = () => snap.sessions.find((x) => x.id === id) || null;

  // What it is doing now: its screen, live. On a phone, while it asks, the
  // screen is folded: the question says the same.
  const nowText = el("p", { class: "now-text" });
  const screen = el("pre", { class: "screen", tabindex: "0" });
  const fresh = el("button", { type: "button", class: "ghost small", hidden: true }, "Show what is new on its screen");
  let drawn = (() => { try { return localStorage.getItem("drawn") === "1"; } catch { return false; } })();
  // A switch: plain text, or the screen with the agent's own layout.
  const how = el("button", { type: "button", role: "switch", class: "switch" },
    el("span", { class: "track", "aria-hidden": "true" }, el("span", { class: "thumb" })), "Exact layout");
  how.onclick = () => {
    drawn = !drawn;
    try { localStorage.setItem("drawn", drawn ? "1" : "0"); } catch { /* this page only */ }
    paintScreen(true);
  };
  const fold = el("button", { type: "button", class: "ghost small fold", "aria-controls": `screen-${id}` });
  const interrupt = el("button", { type: "button", class: "ghost small" }, "Interrupt");
  const nowBox = el("div", { class: "now-box", id: `screen-${id}` }, nowText, screen, el("div", { class: "row" }, fresh, how));
  // Interrupting is asked twice: it stops the agent's turn.
  const stopText = el("span");
  const stopYes = el("button", { type: "button", class: "danger" }, "Interrupt it");
  const stopNo = el("button", { type: "button", class: "ghost" }, "Keep it going");
  const stop = el("div", { class: "confirm", role: "group", "aria-label": "Interrupt", hidden: true },
    stopText, el("div", { class: "row" }, stopNo, stopYes));
  interrupt.onclick = () => { stop.hidden = false; stopNo.focus(); };
  stopNo.onclick = () => { stop.hidden = true; interrupt.focus(); };
  stopYes.onclick = async () => {
    stop.hidden = true;
    if (s) await act(s, "interrupt", {}, `Interrupted ${s.name}.`);
  };
  const now = el("section", { class: "now folded", "aria-labelledby": `now-${id}` },
    el("div", { class: "now-head" }, el("h2", { id: `now-${id}`, text: "On its screen now" }), el("div", { class: "row" }, fold, interrupt)),
    stop, nowBox);
  fold.onclick = () => { now.classList.toggle("folded"); paintFold(); };
  function paintFold() {
    const open = !now.classList.contains("folded");
    set(fold, open ? "Hide its screen" : "Show its screen");
    fold.setAttribute("aria-expanded", String(open));
  }
  paintFold();
  // The screen is not changed under a reader who is in it: what is new
  // waits for them to ask.
  screen.onblur = () => paintScreen();
  function paintScreen(force = false) {
    const t = s?.screen;
    const want = t == null ? "Waiting for its screen…" : (drawn ? t : plain(t)) || "Its screen is empty.";
    screen.classList.toggle("drawn", drawn);
    how.setAttribute("aria-checked", String(drawn));
    if (!force && document.activeElement === screen && screen.textContent && screen.textContent !== want) {
      fresh.hidden = false;
      return;
    }
    fresh.hidden = true;
    set(screen, want);
  }
  fresh.onclick = () => { paintScreen(true); screen.focus(); };

  // What it said last.
  const saidBody = el("div", { class: "prose" });
  const said = el("section", { class: "said" }, el("h2", { text: "It said last" }), saidBody);

  // Its changes: on a wide screen beside the rest, on a phone a page away.
  // On a phone, a card that says what changed and opens the changes.
  const ccCount = el("span", { class: "cc-count" });
  const ccScope = el("span", { class: "cc-scope" });
  const ccFiles = el("ul", { class: "cc-files" });
  const ccMore = el("span", { class: "cc-more" });
  const changesLink = el("a", { class: "changes-card" },
    el("span", { class: "cc-head" }, el("span", { class: "cc-title", text: "Its changes" }), ccCount),
    ccScope, ccFiles, ccMore, el("span", { class: "cc-go", "aria-hidden": "true", text: "›" }));
  const links = el("div", { class: "links" }, changesLink);
  let peekKey = "";
  async function peek(s) {
    const k = `${s.touched}|${s.status}`;
    if (k === peekKey) return;
    peekKey = k;
    let files = [];
    try {
      const r = await api(`/api/sessions/${encodeURIComponent(id)}/changes`);
      files = r.ok ? await r.json() : [];
    } catch { return; }
    sync(ccFiles, files.slice(0, 3), (f) => f.path,
      () => el("li", {}, el("span", { class: "fmark" }), el("span", { class: "path" })),
      (li, f) => {
        li.firstChild.className = `fmark m-${f.mark === "?" ? "new" : f.mark}`;
        set(li.firstChild, markName(f.mark));
        set(li.lastChild, f.path);
      });
    ccFiles.hidden = !files.length;
    set(ccMore, files.length > 3 ? `and ${files.length - 3} more` : "");
    ccMore.hidden = files.length <= 3;
    set(ccCount, files.length ? `${files.length} file${files.length === 1 ? "" : "s"}` : "none yet");
  }
  const changes = changesPanel(id);

  // Its queue.
  const qCount = el("span", { class: "count" });
  const qList = el("ol", { class: "queue" });
  const qHead = el("h2", {});
  const qNote = el("p", { class: "note", text: "cicoria holds these back until you let the queue go there." });
  const queue = el("section", { class: "queued" }, qHead, qNote, qList);

  // The composer, with the receipts of what was done. Its buttons keep
  // their place and their look, whatever the agent is doing.
  const rList = el("ol", { class: "receipts", "aria-label": "What you did from this page" });
  const text = el("textarea", { id: `msg-${id}`, rows: 2, enterkeyhint: "send" });
  const label = el("label", { for: `msg-${id}`, class: "sr-only" });
  const why = el("p", { class: "note why" });
  const later = el("button", { type: "button" }, "Queue");
  const send = el("button", { type: "button", class: "primary" }, "Send now");
  const composer = el("form", { class: "composer" }, rList, label, text, why, el("div", { class: "row end" }, later, send));
  composer.onsubmit = (e) => e.preventDefault();
  const submit = async (how) => {
    const msg = text.value.trim();
    if (!msg || !s) return;
    const ok = how === "send"
      ? await act(s, "send", { text: msg }, `Sent to ${s.name}: “${short(msg, 60)}”.`)
      : await act(s, "queue", { text: msg }, `Queued for ${s.name}: “${short(msg, 60)}”.`);
    if (ok) text.value = "";
  };
  send.onclick = () => submit("send");
  later.onclick = () => submit("queue");
  text.onkeydown = (e) => {
    if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) {
      e.preventDefault();
      (send.hidden || send.disabled ? later : send).click();
    }
  };

  const gone = el("div", { class: "notice", hidden: true },
    el("h1", { text: "This session is not shared any more" }),
    el("p", {}, "It may have been closed, or its sharing turned off in cicoria. ", el("a", { href: "#", text: "See the shared sessions" })));
  // The words come first and the agent's screen and changes after: the
  // order a screen reader reads them in. Each side is a column when there
  // is room.
  const talk = el("div", { class: "talk" }, ask, said, queue);
  const work = el("div", { class: "work" }, now, links, changes.root);
  const panes = el("div", { class: "panes" }, talk, work);
  const body = el("div", { class: "session" }, head, panes, composer);
  const root = el("div", {}, body, gone);

  return {
    root,
    focus: name,
    update() {
      s = current();
      body.hidden = !s;
      gone.hidden = !!s;
      if (!s) return;
      const g = group(s);
      body.className = `session s-${g}`;
      set(name, s.name);
      set(where, [`${s.agent} in ${s.path || s.project}`, s.worktree && `worktree ${s.worktree}`, !s.control && "watch only"].filter(Boolean).join(", "));
      badge.className = `badge ${mark(s)}`;
      set(stateText, state(s));
      interrupt.hidden = !(s.control && s.running && (g === "working" || g === "needs"));
      if (interrupt.hidden) stop.hidden = true;
      set(stopText, `Interrupt ${s.name}? It stops what it is doing and waits for you.`);

      // The question, while there is one.
      const needs = g === "needs";
      ask.hidden = !needs;
      set(askText, s.question || s.prompt || "It is waiting for you.");
      // The menu's own line goes with its choices when the question said
      // something else.
      set(legend, s.prompt || s.question || "Choices");
      // It is shown when it says more than the question above it.
      legend.className = s.prompt && s.question && s.prompt !== s.question ? "prompt" : "sr-only";
      tool.hidden = !needs || !s.tool;
      set(toolName, s.tool ? `${s.tool.name}, in ${s.path || s.project}` : "");
      toolDetail.hidden = !s.tool?.detail;
      set(toolDetail, s.tool?.detail || "");
      const menu = needs && s.control && s.running && s.choices?.length ? s.choices : null;
      choices.hidden = !menu;
      const key = menu ? menu.join("\n") : "";
      if (key !== menuKey) {
        menuKey = key;
        picked = null;
        options.replaceChildren(...(menu || []).map((c, i) => {
          const r = el("input", { type: "radio", name: `choice-${id}`, value: String(i), id: `c-${id}-${i}` });
          r.dataset.label = c;
          return el("label", { class: "option", for: `c-${id}-${i}` }, r, el("span", { text: c }));
        }));
      }
      paintAnswer();
      noMenu.hidden = !needs || !!menu;
      set(noMenu, !s.control ? "Shared to watch: answer it in the terminal, or queue a message."
        : "Answer it in your own words below, or look at its screen for what it waits for.");

      // What it is doing.
      const busy = doing(s);
      nowText.hidden = !busy;
      set(nowText, busy);
      paintScreen();

      said.hidden = !s.answer;
      if (saidBody.dataset.text !== (s.answer || "")) {
        saidBody.dataset.text = s.answer || "";
        saidBody.replaceChildren(...prose(s.answer || ""));
      }

      changesLink.href = `#/s/${encodeURIComponent(id)}/changes`;
      toChanges.href = changesLink.href;
      toChanges.hidden = !s.touched;
      set(toChanges, `${s.touched} file${s.touched === 1 ? "" : "s"} changed`);
      set(ccScope, s.fromStart ? "Since the session started" : "Not committed yet");
      if (!wide.matches) peek(s);
      changes.update(s, wide.matches);

      queue.hidden = !s.queue.length;
      set(qCount, String(s.queue.length));
      qHead.replaceChildren(s.paused ? "Queued, held back" : "Queued for its next turn", " ", qCount);
      qNote.hidden = !s.paused;
      sync(qList, s.queue.map((m, i) => ({ m, i })), ({ m, i }) => `${i}\u0000${m}`,
        () => {
          const t = el("span", { class: "msg" });
          const b = el("button", { type: "button", class: "ghost small" }, "Take back");
          const li = el("li", {}, t, b);
          li.t = t;
          li.b = b;
          return li;
        },
        (li, { m, i }) => {
          set(li.t, m);
          li.b.setAttribute("aria-label", `Take back: ${short(m, 60)}`);
          li.b.onclick = () => act(s, "unqueue", { index: i, text: m }, `Took back from ${s.name}: “${short(m, 60)}”.`);
        });
      // With nothing to read beside it, the screen takes the width.
      panes.classList.toggle("alone", ask.hidden && said.hidden && queue.hidden);

      set(label, `Message to ${s.name}`);
      text.placeholder = needs && !s.choices?.length ? `Answer ${s.name}…` : `Message to ${s.name}…`;
      send.hidden = !(s.control && s.running);
      // Typed into a menu, a message could pick one of its choices.
      const menuUp = needs && !!s.choices?.length;
      send.disabled = menuUp;
      set(why, !s.control ? "Shared to watch: you can queue messages. To answer, send at once or interrupt, give this page control in cicoria."
        : !s.running ? "It is not running: a message waits in the queue."
        : menuUp ? "Send now waits until you pick an answer above."
        : "");
      why.hidden = !why.textContent;

      const rs = receipts.get(id) || [];
      rList.hidden = !rs.length;
      sync(rList, rs, (r) => r.n,
        (r) => el("li", { class: r.failed ? "failed" : "" },
          el("time", { text: r.at.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) }), " ", r.text),
        () => {});
    },
  };
}

// ---- Its changes: the files, then the diff of one. They follow the agent:
// they are read again when it touches a file or its state moves on, and
// at most every few seconds.

function changesPanel(id) {
  let s = null;
  let files = null;
  let filesKey = "";
  let diff = null; // { path, text }
  let last = "";
  let at = 0;
  let loading = false;
  const scope = el("p", { class: "hint" });
  const count = el("span", { class: "count" });
  const title = el("h2", { id: `ch-${id}`, tabindex: "-1" }, "Its changes ", count);
  const body = el("div", { class: "changes-body" });
  const root = el("section", { class: "changes-pane", "aria-labelledby": `ch-${id}` },
    el("div", { class: "ch-head" }, title, scope), body);

  async function load() {
    if (loading) return;
    loading = true;
    at = Date.now();
    let list = [];
    try {
      const r = await api(`/api/sessions/${encodeURIComponent(id)}/changes`);
      list = r.ok ? await r.json() : [];
    } catch { /* kept as it was */ list = files || []; }
    let d = diff;
    if (diff) {
      try {
        const r = await api(`/api/sessions/${encodeURIComponent(id)}/diff?path=${encodeURIComponent(diff.path)}`);
        d = { path: diff.path, text: r.ok ? await r.text() : null };
      } catch { /* kept as it was */ }
    }
    loading = false;
    const k = JSON.stringify([list, d]);
    if (k === filesKey && files !== null) return;
    filesKey = k;
    files = list;
    diff = d;
    paint();
  }
  async function open(path) {
    diff = { path, text: "" };
    filesKey = "";
    at = 0;
    await load();
    body.querySelector(".to-files")?.focus();
  }
  function paint() {
    // Focus stays on the file it was on.
    const had = body.contains(document.activeElement) ? document.activeElement.dataset.path : undefined;
    set(count, files?.length ? `${files.length} file${files.length === 1 ? "" : "s"}` : "");
    if (files === null) {
      body.replaceChildren(el("p", { class: "empty", text: "Loading its changes…" }));
    } else if (diff) {
      body.replaceChildren(
        el("div", { class: "diff-head" },
          el("button", { type: "button", class: "ghost small to-files", onclick: () => {
            const was = diff.path;
            diff = null;
            filesKey = "";
            paint();
            body.querySelector(`[data-path="${CSS.escape(was)}"]`)?.focus();
          } }, "‹ All files"),
          el("h3", { class: "path", text: diff.path })),
        diff.text === null ? el("p", { class: "empty", text: "This file has no changes any more." }) : diffView(diff.text));
    } else if (!files.length) {
      body.replaceChildren(el("p", { class: "empty", text: "No changes." }));
    } else {
      body.replaceChildren(el("ul", { class: "files" }, files.map((f) => el("li", {},
        el("button", { type: "button", "data-path": f.path, onclick: () => open(f.path) },
          el("span", { class: `fmark m-${f.mark === "?" ? "new" : f.mark}`, text: markName(f.mark) }),
          el("span", { class: "path", text: f.path }),
          f.added || f.deleted ? el("span", { class: "count" },
            el("span", { class: "plus", text: `+${f.added}` }), " ", el("span", { class: "minus", text: `−${f.deleted}` }),
            el("span", { class: "sr-only", text: " lines" })) : null)))));
    }
    if (had) body.querySelector(`[data-path="${CSS.escape(had)}"]`)?.focus();
  }
  return {
    root,
    focus: () => title.focus({ preventScroll: true }),
    // update follows the session; active says the panel is in sight.
    update(x, active) {
      s = x;
      set(scope, s.fromStart ? "Since the session started" : "Not committed yet");
      if (!active) return;
      const k = `${s.touched}|${s.status}`;
      if (files === null || k !== last || Date.now() - at > 10000) {
        last = k;
        if (files === null) paint();
        load();
      }
    },
  };
}

// changesView is the changes of a session as a page of their own, for a
// screen without room to show them beside it.
function changesView(id) {
  const name = el("h1", { class: "name", tabindex: "-1" });
  const back = el("a", { class: "back", href: `#/s/${encodeURIComponent(id)}` },
    el("span", { "aria-hidden": "true", text: "‹" }), el("span", { class: "sr-only", text: "Back to the session" }));
  const stateLine = el("p", { class: "where" });
  const panel = changesPanel(id);
  const root = el("div", { class: "changes" },
    el("header", { class: "head" }, back, el("div", { class: "grow" }, name, stateLine)), panel.root);
  return {
    root,
    focus: name,
    update() {
      const s = snap.sessions.find((x) => x.id === id) || null;
      set(name, s ? s.name : "Not shared any more");
      // What it needs stays in sight from here.
      set(stateLine, s ? state(s) : "");
      if (s) panel.update(s, true);
    },
  };
}

function markName(m) {
  return { M: "changed", A: "added", "?": "new", D: "deleted", R: "renamed", U: "conflict" }[m] || "changed";
}

// diffView draws a diff a line at a time, with the numbers of the lines and,
// for a screen reader, whether each was added or removed.
function diffView(text) {
  const rows = [];
  let a = 0, b = 0;
  const line = (cls, la, lb, code, said) => el("div", { class: `dl ${cls}` },
    el("span", { class: "ln", text: la }), el("span", { class: "ln", text: lb }),
    el("code", {}, said ? el("span", { class: "sr-only", text: said }) : null, code || " "));
  for (const l of text.replace(/\n$/, "").split("\n")) {
    const hunk = l.match(/^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/);
    if (hunk) {
      a = +hunk[1];
      b = +hunk[2];
      rows.push(line("hunk", "", "", l));
    } else if (!a && !b || l.startsWith("\\")) {
      rows.push(line("meta", "", "", l));
    } else if (l.startsWith("+")) {
      rows.push(line("add", "", String(b), l.slice(1), `added, line ${b}: `));
      b++;
    } else if (l.startsWith("-")) {
      rows.push(line("del", String(a), "", l.slice(1), `removed, line ${a}: `));
      a++;
    } else {
      rows.push(line("", String(a), String(b), l.slice(1), `line ${b}: `));
      a++;
      b++;
    }
  }
  return el("div", { class: "diff", tabindex: "0", role: "region", "aria-label": "Diff" }, rows);
}

window.addEventListener("hashchange", render);
// Times are relative: they move on their own.
setInterval(update, 30000);
render();
