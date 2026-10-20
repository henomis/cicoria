// The web cockpit: the sessions cicoria shares, from the snapshots it
// streams, and the few things that can be asked of them.
"use strict";

const $ = (sel, root = document) => root.querySelector(sel);
const main = $("#main");

let snap = { workspace: "", sessions: [] };
let source = null;
let watched = null;
// tab is what the session view shows: "answer", "screen" or "changes".
let tab = null;
let view = null;
// loaded says a snapshot came: before it, nothing is known to be missing.
let loaded = false;

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

function api(path, init = {}) {
  return fetch(path, { ...init, headers: { ...init.headers, Authorization: `Bearer ${token}` } });
}

function el(tag, attrs = {}, ...children) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k === "class") e.className = v;
    else if (k.startsWith("on")) e.addEventListener(k.slice(2), v);
    else if (v !== false && v != null) e.setAttribute(k, v === true ? "" : v);
  }
  for (const c of children.flat()) if (c != null) e.append(c);
  return e;
}

// ago tells how long ago a time was, in its largest unit.
function ago(iso) {
  const t = Date.parse(iso);
  if (!t || t < 0) return "";
  const s = (Date.now() - t) / 1000;
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  if (s < 360000) return `${Math.floor(s / 3600)}h`;
  return `${Math.floor(s / 86400)}d`;
}

function statusOf(s) {
  if (s.status === "working" && s.stalled) return { cls: "stalled", text: "working?" };
  return { cls: s.status.replace(" ", "-"), text: s.status };
}

function toast(text) {
  const t = el("div", { class: "toast", role: "status" }, text);
  document.body.append(t);
  setTimeout(() => t.remove(), 2200);
}

async function post(id, kind, body = {}) {
  const r = await api(`/api/sessions/${encodeURIComponent(id)}/${kind}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!r.ok) {
    toast((await r.text()).trim() || `failed: ${r.status}`);
    return false;
  }
  return true;
}

// connect streams the snapshots, with the screen of a session when one is
// looked at.
function connect(watch) {
  if (source && watched === watch && source.readyState !== EventSource.CLOSED) return;
  if (source) source.close();
  watched = watch;
  source = new EventSource(`/events?t=${encodeURIComponent(token)}` + (watch ? `&watch=${encodeURIComponent(watch)}` : ""));
  source.onopen = () => $("#conn").classList.add("on");
  source.onerror = () => {
    $("#conn").classList.remove("on");
    // A stream that is refused is not tried again: the token is not taken.
    if (source.readyState === EventSource.CLOSED) {
      view = null;
      main.replaceChildren(el("p", { class: "empty" }, "This link is not valid any more: open the one cicoria shows."));
    }
  };
  source.onmessage = (e) => {
    snap = JSON.parse(e.data);
    loaded = true;
    update();
  };
}

function route() {
  const m = location.hash.match(/^#\/s\/(.+)$/);
  return m ? decodeURIComponent(m[1]) : null;
}

function render() {
  const id = route();
  main.replaceChildren();
  $("#back").hidden = !id;
  view = id ? sessionView(id) : listView();
  update();
}

function update() {
  view?.update();
}

// The list of the shared sessions.
function listView() {
  connect(null);
  tab = null;
  return {
    update() {
      $("#title").textContent = "cicoria";
      $("#subtitle").textContent = snap.workspace;
      if (!snap.sessions.length) {
        main.replaceChildren(el("p", { class: "empty" }, "No session is shared. In cicoria, open the web cockpit and press space on a session."));
        return;
      }
      main.replaceChildren(el("ul", { class: "sessions" }, snap.sessions.map((s) => {
        const st = statusOf(s);
        const since = ago(s.since);
        return el("li", {},
          el("a", { href: `#/s/${encodeURIComponent(s.id)}`, class: s.status === "needs input" ? "attention" : "" },
            el("span", { class: `badge ${st.cls}`, title: st.text }),
            el("div", { class: "grow" },
              el("div", { class: "name" }, s.name),
              el("div", { class: "meta" }, [st.text, since, s.agent, s.project].filter(Boolean).join(" · "))),
            s.queue.length ? el("span", { class: "queue" }, `⋯${s.queue.length}`) : null));
      })));
    },
  };
}

// One session: what it said or its screen, its changes, its queue, and a
// way to answer it.
function sessionView(id) {
  // The tab is picked once the session is known: until then, no screen.
  connect(null);
  const status = el("div", { class: "status" });
  const tabs = el("div", { class: "tabs", role: "tablist" });
  const body = el("div");
  const keys = el("div", { class: "keys" },
    ["y", "n", "1", "2", "3", "esc", "enter", "up", "down", "left", "right", "tab"].map((k) =>
      el("button", { onclick: () => post(id, "key", { text: k }) },
        { esc: "Esc", enter: "⏎", up: "↑", down: "↓", left: "←", right: "→", tab: "⇥" }[k] || k)));
  const queueHead = el("h2", {});
  const queue = el("ul", { class: "queue" });
  const text = el("textarea", { rows: 2, placeholder: "Message to the agent", enterkeyhint: "send" });
  const sendNow = el("button", { class: "primary" }, "Send");
  const queueIt = el("button", {}, "Queue");
  const composer = el("div", { class: "composer" }, text, el("div", { class: "actions" }, queueIt, sendNow));
  const root = el("div", { class: "session", hidden: true }, status, tabs, body, keys, queueHead, queue, composer);
  const gone = el("p", { class: "empty", hidden: true }, "This session is not shared any more.");
  main.append(root, gone);

  const submit = async (kind) => {
    const msg = text.value.trim();
    if (!msg) return;
    if (await post(id, kind, { text: msg })) {
      text.value = "";
      toast(kind === "queue" ? "queued" : "sent");
    }
  };
  sendNow.onclick = () => submit("send");
  queueIt.onclick = () => submit("queue");

  let shown = null;
  let changes = null;
  let diff = null;

  const loadChanges = async () => {
    const r = await api(`/api/sessions/${encodeURIComponent(id)}/changes`);
    changes = r.ok ? await r.json() : [];
    diff = null;
    paint(true);
  };
  const openDiff = async (path) => {
    const r = await api(`/api/sessions/${encodeURIComponent(id)}/diff?path=${encodeURIComponent(path)}`);
    diff = { path, text: r.ok ? await r.text() : "This file has no changes any more." };
    paint(true);
  };

  // paint draws the tab. Unless forced, the changes are left as they are,
  // so that a diff being read is not redrawn under the reader.
  function paint(force) {
    const s = snap.sessions.find((x) => x.id === id);
    if (tab === "changes" && !force && shown === "changes") return;
    shown = tab;
    if (tab === "answer") {
      body.replaceChildren(el("div", { class: "card" },
        el("p", { class: "answer" }, s?.answer || "No answer yet: it comes at the end of a turn.")));
    } else if (tab === "screen") {
      body.replaceChildren(el("pre", { class: "screen" }, s?.screen || "…"));
    } else if (diff) {
      body.replaceChildren(
        el("div", { class: "actions" }, el("button", { onclick: () => { diff = null; paint(true); } }, "‹ Files")),
        el("pre", { class: "diff" }, diff.text.split("\n").map((l) => el("span", {
          class: l.startsWith("@@") ? "hunk"
            : /^(diff |index |--- |\+\+\+ )/.test(l) ? "head"
            : l.startsWith("+") ? "add" : l.startsWith("-") ? "del" : "",
        }, l || " "))));
    } else if (changes === null) {
      body.replaceChildren(el("p", { class: "empty" }, "Loading…"));
    } else if (!changes.length) {
      body.replaceChildren(el("p", { class: "empty" }, "No changes."),
        el("div", { class: "actions" }, el("button", { onclick: loadChanges }, "Refresh")));
    } else {
      body.replaceChildren(
        el("ul", { class: "files" }, changes.map((c) => el("li", {},
          el("button", { onclick: () => openDiff(c.path) },
            el("span", { class: `mark ${c.mark}` }, c.mark),
            el("span", { class: "path" }, c.path),
            el("span", { class: "count" }, `+${c.added} −${c.deleted}`))))),
        el("div", { class: "actions" }, el("button", { onclick: loadChanges }, "Refresh")));
    }
  }

  const select = (name) => {
    tab = name;
    connect(name === "screen" ? id : null);
    if (name === "changes") loadChanges();
    update();
  };

  return {
    update() {
      const s = snap.sessions.find((x) => x.id === id);
      root.hidden = !s;
      gone.hidden = !!s || !loaded;
      if (!s) {
        $("#title").textContent = loaded ? "Not shared" : "cicoria";
        $("#subtitle").textContent = "";
        return;
      }
      // An agent that does not tell its answers shows its screen.
      if (!tab) return select(s.answer ? "answer" : "screen");

      $("#title").textContent = s.name;
      $("#subtitle").textContent = [s.agent, s.project].join(" · ");
      const st = statusOf(s);
      status.className = `status ${st.cls}`;
      status.replaceChildren(el("span", { class: `badge ${st.cls}` }), el("strong", {}, st.text),
        ago(s.since) ? el("span", {}, `for ${ago(s.since)}`) : null);
      tabs.replaceChildren(...[["answer", "Answer"], ["screen", "Screen"], ["changes", "Changes"]].map(([k, label]) =>
        el("button", { role: "tab", "aria-selected": String(tab === k), onclick: () => select(k) }, label)));
      paint(false);

      queueHead.replaceChildren(`Queue (${s.queue.length})${s.paused ? " · paused" : ""}`);
      queueHead.hidden = queue.hidden = !s.queue.length;
      queue.replaceChildren(...s.queue.map((m) => el("li", {}, el("span", {}, m))));
      sendNow.disabled = !s.running;
      for (const b of keys.children) b.disabled = !s.running;
    },
  };
}

window.addEventListener("hashchange", render);
// Times are relative: they move on their own.
setInterval(update, 30000);
render();
