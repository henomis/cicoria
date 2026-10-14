# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

cicoria is a terminal workspace (Go, Bubble Tea v2) that runs several coding agents side by side, each in its own pseudo-terminal pane, with panels for sessions, files, git changes and a message queue. `README.md` is the user-facing description of every feature.

## Commands

```sh
make build      # build ./cicoria
make check      # go vet, all tests, gofmt check: run before a commit
make lint       # golangci-lint with its default linters, as the CI runs it
make test       # tests only
make race       # tests with the race detector
make test-real  # the one test that embeds the real Claude Code (needs a trusted folder)
make site       # serve web/ on http://localhost:8000
```

A single test:

```sh
env -u CICORIA_SOCK -u CICORIA_SESSION go test ./internal/ui -run TestPagerWrap
env -u CICORIA_SOCK -u CICORIA_SESSION go test . -run TestReviewSinceTheStart -v
```

The `env -u` matters when working from inside a cicoria session: the tests start cicoria themselves, and those two variables would point their hooks at the instance that is already running. The Makefile targets do this already.

CI (`.github/workflows/checks.yml`) runs `golangci-lint` and `make check` on pushes and pull requests, skipping changes that only touch `*.md` or `web/`. `web.yml` publishes `web/` to GitHub Pages on every push to `main`. `release.yml` runs GoReleaser (`.goreleaser.yaml`) on a `v*.*.*` tag and publishes the binaries for Linux and macOS; `goreleaser release --snapshot --clean` builds them locally into `dist/`. `web/install.sh`, served by GitHub Pages, is what `curl … | sh` runs: it downloads the archive and the checksums of a release by the names GoReleaser gives them.

## Architecture

Dependencies point one way: `main.go` → `internal/app` → everything else. `internal/ui` knows nothing about agents, sessions or git.

- **`main.go`** is two programs in one binary. `cicoria [dir]` opens the workspace. `cicoria hook` is what an agent's hooks call: it reads the hook payload on stdin and forwards it to the running workspace, and it must always succeed so that it never gets in the agent's way.
- **`internal/app`** is the single Bubble Tea model (`Model` in `app.go`). It owns all state: the sessions, which pane has the keys (`focus`), what is on top of the workspace (`mode`: full-screen view, palette, help), and the one-line prompt/confirm in the status bar. `keys.go` routes input, `commands.go` holds the table of prefix commands, `view.go` draws, `fullview.go` is the full-screen file/diff view with notes, `queue.go` the message queue. `platform.go` tells WSL from Linux (`system()`): Windows is supported through WSL2 only, where the desktop a file is opened on is the one of Windows.
- **`internal/agent`** hides everything specific to one agent behind `Adapter`: how to build its command line, whether it reports its status, how to mention a file, and how to turn a hook event into an `Update`. `claude.go`, `copilot.go` and `codex.go` are the full integrations; `generic.go` is any other command line.
- **`internal/session`** is one agent plus its terminal: status, queue, notes, and the commit its changes are counted from. `store.go` saves the workspace as JSON under `$XDG_STATE_HOME/cicoria/<hash of the directory>/`. The file records its directory, so that the other workspaces can be listed, and a `lock` file next to it is held while the workspace is open.
- **`internal/term`** runs a command in a PTY and keeps an emulated screen of its output (`charmbracelet/x/vt`), so the agent's own interface can be drawn inside a pane.
- **`internal/ipc`** is the unix socket hook events arrive on.
- **`internal/git`** reads the work tree by running the `git` command line.
- **`internal/ui`** holds the widgets (pager, tree, palette, input, boxes, syntax highlighting). They keep their own state and render to strings.

### How a session's status is known

For Claude Code, cicoria starts `claude` with `--settings` carrying hooks that run `cicoria hook`. The child process gets `CICORIA_SOCK` and `CICORIA_SESSION` in its environment; the hook sends `{session, payload}` to that socket; `app` hands the event to the adapter's `MapEvent`, and the resulting `Update` changes the session (status, turn ended, file touched, conversation ID). Adapters that do not report status (`ReportsStatus() == false`) have it guessed in `session` from terminal activity: output means working, quiet means idle, the bell means it needs input.

GitHub Copilot CLI is started with `--plugin-dir` pointing at a plugin, written under the user's cache directory, whose hooks run `"$CICORIA_BIN" hook <name>`: its payloads do not say which hook they are from, so the name travels on the command line and in `ipc.Event.Name`. It runs no hook before the first message (`ReportsReady() == false`), so a starting session becomes idle once its screen goes quiet, and none when a turn fails for good, which shows as `working?`.

Codex CLI is started with one `-c hooks.<Event>=…` option for each hook, which run `"$CICORIA_BIN" hook`. Codex CLI runs a hook only once the user has trusted it, asks when it starts with hooks it does not know, and remembers the answer by the hook's command: that is why the command does not hold the path of the binary. Like Copilot CLI it runs no hook before the first message, and none when a turn fails.

Queued messages depend on this: a message goes out when an `Update` says the turn ended, which only Claude Code, Copilot CLI and Codex CLI report.

A queued message can be a question from another session (`session.Message.ReplyTo`). The session that gets it remembers it as `Answering`, and `Update.Answer`, what the agent said last in the turn that ended, is queued back for the session that asked. Claude Code and Codex CLI give it in the payload of their `Stop` hook. Copilot CLI does not (`ReportsAnswer() == false`), so it cannot be asked: no transcript of an agent is read, only looked for to know whether a conversation can be resumed. `Session.Settle` decides whether a turn that ended is the one the question started: it is not when another message was taken first (`Update.TurnStarted`), which is how an interrupted turn shows. Claude Code names the message a turn is for (`Update.Prompt`), which settles it exactly.

### How a conversation survives a restart

The agent process stops with cicoria. A Claude session is started with `--session-id <id>` and, when a transcript for that ID exists under Claude Code's config directory, restarted with `--resume`. After `/clear` Claude Code reports a new conversation ID through the hooks, which is saved as the session's `Conversation` and resumed in preference. Copilot CLI works the same way with `--session-id` alone, which both names a new session and continues an existing one; its transcripts are under `$COPILOT_HOME/session-state`. Codex CLI names its conversations itself: the ID it reports is saved as `Conversation`, and the session is restarted with `resume <id>` when a transcript for it exists under `$CODEX_HOME/sessions`.

### Changes of a session

The changes panel shows either what is not committed or everything since `Session.Base`, the commit the work tree was on when the session was created. `Session.Uncommitted` picks between them; it starts from the `changes` setting, is toggled with `s` in the panel, and is not saved. `app.refresh` re-reads the directory of the session on screen in the background every two seconds, except while the terminal is not focused: it is read again when the focus comes back. The comparison is of the directory, not of the agent, so two sessions on one directory see each other's changes. That is why a session created in the work tree of another one is offered a git worktree of its own (`askWorktree`, `addWorktree`): `Session.Worktree` remembers it, and `closeSession` offers to remove it when it is clean.

### The pager

`ui.Pager` is indexed by line: cursor, selection, notes, hunk marks and search matches are all line numbers, and `fullView.refs` maps each shown line back to a line of the file. Soft wrap (`Pager.Wrap`) keeps that: a line may take several screen rows, and only scrolling, the cursor's visibility and the click-to-line mapping (`rows`, `topFor`, `bottom`, `LineAt`) know about rows.

## Conventions

- **A command is one entry in the `commands` table** in `internal/app/commands.go`; the prefix key and the command palette both read it, and its `keepsView` says whether it runs over the full-screen view.
- **Keys**: the prefix works on every screen, the full-screen view included, except while text is typed (a prompt, the palette, a search) or a question or help is open. An action offered both after the prefix and in a panel or the view uses the same letter in both (`m` queues a message, `t` sends the notes, `,` renames, `r` restarts). A letter does not mean opposite things in two places: `d` opens a diff, `x` deletes or closes, `o` opens with the system's program. Moving follows vim and less, which is why `q` and `esc` both go back. In a list `c` creates (the queue adds with `m`, as `ctrl+a m` does) and `x` deletes; `enter` acts on the row, except in the queue, where it sends the next message.
- **A user-visible change is documented in four places**: the help screen in `internal/app/view.go`, `README.md`, `web/docs.html` and, for headline features, `web/index.html`. The website is plain HTML, CSS and JavaScript with no build step.
- **Settings**: a new key goes in `config.Config` with its default in `Default()`, and in the README and `web/docs.html` tables. `Load` rejects unknown keys and invalid values, so every key is a compatibility promise; keep the list short.
- **Colours come from the terminal's 16-colour palette** (`internal/ui/style.go`) so that they follow the user's theme. Do not introduce fixed RGB colours in the app.
- **End-to-end tests** (`e2e_test.go`) build the binary and drive it in a PTY against a scratch git project, with shell scripts standing in for Claude Code, Copilot CLI and Codex CLI that emit real hook events. `w.see(...)` and `w.gone(...)` wait for text on the screen; `w.set(...)` adds a line to the config before `w.launch`. Text asserted with `gone` must not also appear in another panel, such as a file name in the file tree.
- **Comments and commit messages** are short plain sentences that say what something is for. A commit subject is an imperative sentence about the user-visible change, and the body explains it in a few lines.
