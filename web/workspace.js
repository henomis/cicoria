// A small cicoria workspace that runs on the page: four sessions follow a
// script, and the visitor jumps to the one that needs input and answers it,
// with the same keys as the real thing.
(() => {
  const root = document.getElementById("ws");
  if (!root) return;

  const you = (t) => ({ k: "you", t: "› " + t });
  const out = (t) => ({ k: "out", t: "● " + t });
  const dim = (t) => ({ k: "dim", t });
  const ask = (t) => ({ k: "ask", t });

  const sessions = [
    {
      name: "api", agent: "claude", dir: "~/src/shop", branch: "main", age: 190,
      steps: [
        { status: "working", dur: 8, lines: [you("Add GET /orders/{id}/receipt, with a test"), dim("Read 3 files"), out("Writing the handler in api/orders.go…")],
          files: [["M", "api/orders.go", 31, 2]], queue: ["then update the OpenAPI spec"] },
        { status: "working", dur: 9, lines: [out("Added the endpoint and api/receipt_test.go. go test ./... passes."), you("then update the OpenAPI spec"), out("Updating openapi.yaml…")],
          files: [["M", "api/orders.go", 42, 3], ["A", "api/receipt_test.go", 58, 0], ["M", "openapi.yaml", 24, 0]], queue: [] },
        { status: "done", dur: 16, lines: [out("openapi.yaml documents the receipt endpoint and its 404."), dim("Worked for 2m 14s")] },
      ],
    },
    {
      name: "flaky-test", agent: "copilot", dir: "~/src/worker", branch: "fix/flaky", age: 75,
      steps: [
        { status: "working", dur: 5, lines: [you("TestRetryQueue fails now and then. Find out why."), dim("Ran 2 shell commands"), out("Running the test 20 times…")], files: [], queue: [] },
        { status: "input", lines: [out("It failed 3 times in 20: two tests share one temp dir."), ask("Run go test -race -count=50 ./queue to confirm?")],
          choices: ["Yes", "No, fix it now"] },
        { status: "working", dur: 6, lines: [out("Giving each test its own t.TempDir()…")], files: [["M", "queue/retry_test.go", 6, 9]] },
        { status: "done", dur: 18, lines: [out("50 runs of 50 pass with -race. Nothing is committed yet.")] },
      ],
    },
    {
      name: "checkout-ui", agent: "codex", dir: "~/src/shop.worktrees/checkout-ui", branch: "cicoria/checkout-ui", age: 410,
      steps: [
        { status: "working", dur: 12, lines: [you("Build the payment step of the checkout"), dim("Explored 6 files"), out("Adding PaymentStep.tsx and its form state…")],
          files: [["A", "web/src/checkout/PaymentStep.tsx", 118, 0], ["M", "web/src/checkout/Checkout.tsx", 9, 2]], queue: [] },
        { status: "input", lines: [ask("Allow the command npm install @stripe/stripe-js?")], choices: ["Yes", "Yes, and don't ask again", "No"] },
        { status: "working", dur: 8, lines: [dim("Ran npm install"), out("Wiring the card element into the form…")],
          files: [["A", "web/src/checkout/PaymentStep.tsx", 141, 0], ["M", "web/src/checkout/Checkout.tsx", 9, 2], ["M", "web/package.json", 1, 0]] },
        { status: "done", dur: 20, lines: [out("The payment step renders, and its tests pass.")] },
      ],
    },
    {
      name: "docs", agent: "claude", dir: "~/src/site", branch: "main", age: 620,
      steps: [
        { status: "idle", dur: 7, lines: [you("Fix the broken links in the install guide"), out("Fixed 4 links in docs/install.md.")], files: [["M", "docs/install.md", 4, 4]], queue: [] },
        { status: "working", dur: 10, lines: [you("Now check the API reference against openapi.yaml"), out("Comparing 23 endpoints…")] },
        { status: "done", dur: 22, lines: [out("Two endpoints were missing from the reference. I added them.")], files: [["M", "docs/install.md", 4, 4], ["M", "docs/api.md", 37, 0]] },
      ],
    },
  ];

  const marks = { working: "●", input: "▲", done: "✓", idle: "○" };
  const labels = { working: "working", input: "needs input", done: "finished", idle: "idle" };

  let current = 0;
  let armed = false;
  let armTimer = 0;
  let message = "";
  let messageTimer = 0;

  for (const s of sessions) enter(s, 0, true);

  function enter(s, i, first) {
    if (i === 0) s.lines = [];
    s.i = i;
    const step = s.steps[i];
    s.left = step.dur || 0;
    s.lines = s.lines.concat(step.lines).slice(-7);
    if (step.files) s.files = step.files;
    if (step.queue) s.queue = step.queue.slice();
    if (!first || s.since === undefined) s.since = Date.now() - (first ? s.age * 1000 : 0);
    s.seen = sessions[current] === s;
    if (step.status === "input" && !first) flash(s);
  }

  function step(s) { return s.steps[s.i]; }

  function shown(s) {
    const st = step(s).status;
    return st === "done" && s.seen ? "idle" : st;
  }

  function advance(s) { enter(s, (s.i + 1) % s.steps.length); }

  function age(s) {
    const sec = Math.floor((Date.now() - s.since) / 1000);
    return sec < 60 ? sec + "s" : Math.floor(sec / 60) + "m";
  }

  // The parts of the workspace are built once and filled on each tick.
  const list = root.querySelector(".ws-sessions");
  const rows = sessions.map((s, n) => {
    const li = document.createElement("li");
    const b = document.createElement("button");
    b.type = "button";
    b.innerHTML = `<span class="n">${n + 1}</span><span class="mk"></span><span class="nm">${s.name}</span><span class="age"></span>`;
    b.addEventListener("click", () => go(n));
    li.append(b);
    list.append(li);
    return b;
  });
  const title = root.querySelector(".ws-pane > .tp-t");
  const output = root.querySelector(".ws-out");
  const filesTitle = root.querySelector(".ws-changes > .tp-t");
  const files = root.querySelector(".ws-files");
  const queueTitle = root.querySelector(".ws-queue-t");
  const queue = root.querySelector(".ws-queue");
  const fileTree = root.querySelector(".ws-tree");
  const need = root.querySelector(".ws-status .need");
  let paneKey = "";

  function flash(s) {
    const b = rows[sessions.indexOf(s)];
    if (!b) return;
    b.classList.remove("ws-flash");
    void b.offsetWidth;
    b.classList.add("ws-flash");
  }

  function render() {
    sessions.forEach((s, n) => {
      const st = shown(s);
      const b = rows[n];
      const mk = b.querySelector(".mk");
      mk.textContent = marks[st];
      mk.className = "mk s-" + st;
      b.querySelector(".age").textContent = age(s);
      b.setAttribute("aria-current", n === current ? "true" : "false");
      b.setAttribute("aria-label", `${s.name}, ${labels[st]}`);
    });

    const s = sessions[current];
    const st = shown(s);
    title.textContent = `${s.name} · ${s.agent} · ${labels[st]} · ${s.dir} · ${s.branch}`;

    const key = `${current}:${s.i}:${s.lines.length}`;
    if (key !== paneKey) {
      paneKey = key;
      output.replaceChildren(...s.lines.map((l) => {
        const p = document.createElement("p");
        p.className = l.k;
        p.textContent = l.t;
        return p;
      }));
      const choices = step(s).choices;
      if (choices) {
        const box = document.createElement("div");
        box.className = "ws-choices";
        choices.forEach((c, n) => {
          const b = document.createElement("button");
          b.type = "button";
          b.textContent = `${n + 1}. ${c}`;
          b.addEventListener("click", () => answer(n));
          box.append(b);
        });
        output.append(box);
      }

      const dir = s.dir.split("/").pop();
      filesTitle.textContent = `Changes (${s.files.length})`;
      files.replaceChildren(...s.files.map(([k, path, add, del]) => {
        const li = document.createElement("li");
        li.innerHTML = `<span class="${k}">${k}</span><span class="nm"></span><span><span class="add">${add ? "+" + add : ""}</span> <span class="del">${del ? "-" + del : ""}</span></span>`;
        li.querySelector(".nm").textContent = path;
        return li;
      }));
      queueTitle.textContent = `Queue (${s.queue.length})`;
      queue.replaceChildren(...(s.queue.length ? s.queue : ["empty"]).map((q) => {
        const li = document.createElement("li");
        li.textContent = s.queue.length ? "▸ " + q : q;
        if (!s.queue.length) li.className = "s-idle";
        return li;
      }));
      fileTree.textContent = `Files · ${dir}`;
    }

    const waiting = sessions.filter((o, n) => n !== current && step(o).status === "input");
    if (message) need.textContent = message;
    else if (armed) need.textContent = "ctrl+a  …";
    else if (waiting.length) need.textContent = `▲ ${waiting.map((o) => o.name).join(", ")} ${waiting.length > 1 ? "need" : "needs"} input`;
    else need.textContent = "";
  }

  function say(text) {
    message = text;
    clearTimeout(messageTimer);
    messageTimer = setTimeout(() => { message = ""; render(); }, 2200);
  }

  function go(n) {
    current = (n + sessions.length) % sessions.length;
    const s = sessions[current];
    s.seen = true;
    render();
  }

  function jump() {
    for (let d = 1; d <= sessions.length; d++) {
      const n = (current + d) % sessions.length;
      if (step(sessions[n]).status === "input" && n !== current) return go(n);
    }
    say("No session needs input");
    render();
  }

  function answer(n) {
    const s = sessions[current];
    const choices = step(s).choices;
    if (!choices || !choices[n]) return;
    s.lines = s.lines.concat([you(choices[n])]);
    advance(s);
    s.seen = true;
    render();
  }

  // A session that needs input waits for an answer; the others follow their script.
  function beat() {
    for (const s of sessions) {
      if (step(s).status === "input") continue;
      if (--s.left <= 0) advance(s);
    }
    render();
  }
  let tick = setInterval(beat, 1000);

  // Keys work while the workspace is in view, as they would in a terminal.
  let visible = false;
  new IntersectionObserver(([e]) => { visible = e.isIntersecting; }, { threshold: 0.35 }).observe(root);

  document.addEventListener("keydown", (e) => {
    if (!visible || e.altKey || e.metaKey) return;
    const t = e.target;
    if (t instanceof HTMLElement && (t.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName))) return;
    if (e.ctrlKey && e.key.toLowerCase() === "a") {
      e.preventDefault();
      armed = true;
      clearTimeout(armTimer);
      armTimer = setTimeout(() => { armed = false; render(); }, 2500);
      render();
      return;
    }
    if (e.ctrlKey) return;
    if (armed) {
      armed = false;
      clearTimeout(armTimer);
      e.preventDefault();
      if (e.key === "a") jump();
      else if (e.key === "n") go(current + 1);
      else if (e.key === "p") go(current - 1);
      else if (/^[1-4]$/.test(e.key)) go(+e.key - 1);
      else if (e.key === "?") { say("The real help screen lists every key"); render(); }
      else render();
      return;
    }
    if (root.contains(document.activeElement) && /^[1-3]$/.test(e.key) && step(sessions[current]).choices) {
      e.preventDefault();
      answer(+e.key - 1);
    }
  });

  document.addEventListener("visibilitychange", () => {
    clearInterval(tick);
    if (!document.hidden) tick = setInterval(beat, 1000);
  });

  render();
})();
