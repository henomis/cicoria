// A drawing of the cicoria workspace, 100 columns by 21 rows, that answers
// to the keys and clicks the real one does for switching sessions.
(() => {
  // The web font has only letters: borders and symbols come from whatever
  // font the device has, which on a phone is rarely as wide. Each of them
  // gets a cell of its own, so that the columns stay in line.
  const wide = /[^\x00-\x7f]/, wideAll = new RegExp(wide, "g");
  for (const pre of document.querySelectorAll(".abilities pre")) {
    const walker = document.createTreeWalker(pre, NodeFilter.SHOW_TEXT);
    const texts = [];
    while (walker.nextNode()) texts.push(walker.currentNode);
    for (const text of texts) {
      if (!wide.test(text.data)) continue;
      const cells = document.createDocumentFragment();
      for (const c of text.data) {
        const cell = wide.test(c) ? document.createElement("i") : document.createTextNode(c);
        cell.textContent = c;
        cells.append(cell);
      }
      text.replaceWith(cells);
    }
  }

  const screen = document.getElementById("screen");
  if (!screen) return;
  const out = screen.querySelector("pre");

  const COLS = 100, LEFT = 30, RIGHT = 28, MID = COLS - LEFT - RIGHT, ROWS = 20;

  // A row is a list of [text, class] runs.
  const t = (text, cls = "") => [text, cls];
  const width = (row) => row.reduce((n, [text]) => n + [...text].length, 0);
  const fit = (row, w) => {
    const fitted = [];
    let left = w;
    for (const [text, cls] of row) {
      const chars = [...text];
      if (left <= 0) break;
      fitted.push([chars.slice(0, left).join(""), cls]);
      left -= Math.min(chars.length, left);
    }
    if (left > 0) fitted.push([" ".repeat(left), ""]);
    return fitted;
  };
  // spread puts one run at the left edge and others at the right one.
  const spread = (left, right, w) => [...fit([left], w - width(right)), ...right];
  const file = (name, mark, touched) => spread(t(name, touched ? "b" : ""), mark ? [mark] : [], LEFT - 2);
  const change = (mark, path, added, deleted) => [
    t(mark, mark === "A" ? "g" : "w"),
    ...spread(t(" " + path), [t("+" + added, "g"), ...(deleted ? [t(" -" + deleted, "r")] : [])], RIGHT - 3),
  ];
  const M = t("M", "w"), A = t("A", "g"), dot = t("•", "d");

  const box = (title, rows, w, h, focused = false) => {
    const edge = focused ? "b" : "d";
    // A title too long for the border loses its end.
    const room = w - 6;
    if ([...title].length > room) title = [...title].slice(0, room - 1).join("") + "…";
    const top = "╭─ " + title + " ";
    const lines = [[t(top + "─".repeat(w - 1 - [...top].length) + "╮", edge)]];
    for (let i = 0; i < h - 2; i++) {
      lines.push([t("│", edge), ...fit(rows[i] || [], w - 2), t("│", edge)]);
    }
    lines.push([t("╰" + "─".repeat(w - 2) + "╯", edge)]);
    return lines;
  };

  const sessions = [
    {
      name: "auth-refactor", status: "working", dir: "~/src/app", since: "3m",
      pane: [
        [], [t(" > move token checks into api/token.go", "s")], [],
        [t(" ● ", "g"), t("I'll split the validation out of")], [t("   auth.go and keep the handlers thin.")], [],
        [t(" ● ", "g"), t("Update(api/auth.go)")], [t("   removed 3 lines, added 42", "d")], [],
        [t(" ● ", "g"), t("Write(api/token.go)")], [t("   18 lines", "d")], [],
        [t(" * ", "w"), t("Running go test ./api/...  41s", "w")],
      ],
      files: [
        file("▾ api/", dot, true), file("   auth.go", M, true), file("   token.go", A, true),
        file("▸ cmd/", null, true), file("  go.mod", M), file("  main.go"),
      ],
      changes: ["Changes (3)", [change("M", "api/auth.go", 42, 3), change("A", "api/token.go", 18), change("M", "go.mod", 1)]],
      queue: ["then run the linter", "update the changelog"],
      tokens: "ctx 82k · in 4.2M · out 38k",
    },
    {
      name: "fix-flaky-test", status: "needs input", dir: "~/src/worker", since: "6m",
      pane: [
        [], [t(" > TestRetry fails one run in twenty", "s")], [],
        [t(" ● ", "g"), t("The backoff reads the clock twice.")], [t("   Let me reproduce it first.")], [],
        [t(" ● ", "g"), t("Bash(go test -run TestRetry ./retry)")], [],
        [t("   Allow this command?", "w")], [t("   > 1. Yes", "s")],
        [t("     2. Yes, and don't ask again")], [t("     3. No")],
      ],
      files: [
        file("▾ retry/", dot, true), file("   backoff.go"), file("   retry_test.go", M, true),
        file("  go.mod"), file("  worker.go"),
      ],
      changes: ["Changes (1)", [change("M", "retry/retry_test.go", 6, 2)]],
      queue: [],
      tokens: "ctx 31k · in 910k · out 9k",
    },
    {
      name: "docs-pass", status: "idle", dir: "~/src/site", since: "12m", unseen: true,
      pane: [
        [], [t(" > bring the usage page up to date", "s")], [],
        [t(" ● ", "g"), t("Updated README.md and docs/usage.md")], [t("   in two commits. The examples now")],
        [t("   match the flags of the last release.")], [], [t(" > ", "s"), t("█", "d")],
      ],
      files: [file("▾ docs/", dot, true), file("   usage.md", M, true), file("  README.md", M, true)],
      changes: ["Changes (2) · 2 commits", [change("M", "README.md", 30, 12), change("M", "docs/usage.md", 9)]],
      queue: [],
      tokens: "ctx 12k · in 240k · out 5k",
    },
  ];

  const badge = (s) => {
    if (s.status === "working") return t("●", "g");
    if (s.status === "needs input") return t("▲", "w");
    return s.unseen ? t("✓", "g") : t("○", "d");
  };

  // The chicory that grows when cicoria opens, stage by stage.
  const plant = [
    [[], ["      .      "]],
    [[], ["      ,      ", "      |      "]],
    [[], ["      |/     ", "     \\|      ", "      |      "]],
    [["      o      "], ["      |/     ", "     \\|      ", "      |      "]],
    [["     \\|/     ", "    -(@)-    ", "     /|\\     "], ["      |/     ", "     \\|      ", "      |      "]],
    [["    \\ | /    ", "  '. \\|/ .'  ", " ---( @ )--- ", "  .' /|\\ '.  ", "    / | \\    "], ["      |/     ", "     \\|      ", "      |      "]],
  ];
  const bloom = (p) => {
    const [flower, stem] = plant[Math.min(Math.floor(p * 1.25 * plant.length), plant.length - 1)];
    const name = "c i c o r i a";
    const rows = Array.from({ length: ROWS - 2 }, () => []);
    const ground = 12, x = " ".repeat((MID - 2 - 13) >> 1);
    stem.forEach((l, i) => (rows[ground - stem.length + i] = [t(x + l, "g")]));
    flower.forEach((l, i) => (rows[ground - stem.length - flower.length + i] = [t(x + l, "b")]));
    rows[ground] = [t(x + "~".repeat(13), "d")];
    rows[ground + 2] = [t(x + name.slice(0, Math.floor(p * 1.4 * name.length)), "s")];
    return rows;
  };

  let active = 0, intro = 0;

  const draw = () => {
    const s = sessions[active];
    const list = sessions.map((x, i) => {
      const row = [t(String(i + 1), "d"), t(" "), badge(x), t(" "), t(x.name, i === active ? "s" : "")];
      const tail = x.since + " " + x.dir.split("/").pop();
      return { pick: i, row: [...fit(row, LEFT - 2 - tail.length), t(tail, "d")] };
    });
    const left = [
      ...box("Sessions", list.map((l) => l.row), LEFT, sessions.length + 2),
      ...box("Files · " + s.dir.split("/").pop(), s.files, LEFT, ROWS - sessions.length - 2),
    ];
    const title = intro < 1 ? "cicoria" : `${s.name} · claude · ${s.status} · ${s.dir}`;
    const mid = box(title, intro < 1 ? bloom(intro) : s.pane, MID, ROWS, true);
    const queueH = s.queue.length ? s.queue.length + 2 : 0;
    const right = [
      ...box(s.changes[0], s.changes[1], RIGHT, ROWS - queueH),
      ...(queueH ? box(`Queue (${s.queue.length})`, s.queue.map((q, i) => [t(i ? "  " : "▸ ", "b"), t(q)]), RIGHT, queueH) : []),
    ];

    const waiting = sessions.filter((x, i) => x.status === "needs input" && i !== active);
    const note = waiting.map((x) => ` ▲ ${x.name} needs input`).join("");
    const hint = s.tokens + " · ctrl+a ? help ";
    const status = [t(note, "w"), t(" ".repeat(Math.max(COLS - [...note].length - [...hint].length, 1))), t(hint, "d")];

    const esc = (text) => text.replace(/[&<>]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;" })[c]);
    const cells = (text) => esc(text).replace(wideAll, "<i>$&</i>");
    const html = (row) => row.map(([text, cls]) => (cls ? `<span class="${cls}">${cells(text)}</span>` : cells(text))).join("");

    const lines = [];
    for (let y = 0; y < ROWS; y++) {
      let l = html(left[y]);
      // A session row is one click away, like in the real list.
      if (y >= 1 && y <= sessions.length) {
        const i = y - 1, row = left[y];
        l = html([row[0]]) + `<span class="pick" data-pick="${i}">` + html(row.slice(1, -1)) + "</span>" + html([row[row.length - 1]]);
      }
      lines.push(l + html(mid[y]) + html(right[y]));
    }
    lines.push(html(fit(status, COLS)));
    out.innerHTML = lines.join("\n");

    const names = sessions.map((x, i) => `${i + 1} ${x.name}, ${x.status}`).join("; ");
    screen.setAttribute("aria-label", `A drawing of the cicoria workspace. Sessions: ${names}. On screen: ${s.name}.`);
  };

  const go = (i) => {
    active = (i + sessions.length) % sessions.length;
    sessions[active].unseen = false;
    intro = 1;
    draw();
  };

  screen.addEventListener("click", (e) => {
    const pick = e.target.closest("[data-pick]");
    if (pick) go(Number(pick.dataset.pick));
  });
  screen.addEventListener("keydown", (e) => {
    if (e.ctrlKey || e.metaKey || e.altKey) return;
    if (e.key >= "1" && e.key <= String(sessions.length)) go(Number(e.key) - 1);
    else if (e.key === "n" || e.key === "ArrowDown") go(active + 1);
    else if (e.key === "p" || e.key === "ArrowUp") go(active - 1);
    else if (e.key === "a") go(sessions.findIndex((x) => x.status === "needs input"));
    else return;
    e.preventDefault();
  });

  // The one moment of motion: the flower opens, once, as it does in cicoria.
  if (matchMedia("(prefers-reduced-motion: reduce)").matches) {
    intro = 1;
    draw();
  } else {
    // Until the browser animates, which a tab in the background or a preview
    // of the page never does, the chicory stands in full flower.
    const length = 1400, grown = 0.99;
    let start;
    const frame = (now) => {
      if (intro >= 1) return;
      start ??= now;
      intro = Math.min((now - start) / length, 1);
      draw();
      if (intro < 1) requestAnimationFrame(frame);
    };
    intro = grown;
    draw();
    requestAnimationFrame(frame);
  }
})();
