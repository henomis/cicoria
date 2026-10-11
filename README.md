# cicoria

A terminal workspace for the AI-first engineer: run several coding agents side by side, see which one needs you, and review what they changed, without leaving the terminal.

```
╭─ Sessions ───────────────╮╭─ auth-refactor · claude · working ─────╮╭─ Changes (3) ─────────╮
│1 ● auth-refactor   3m app││                                        ││M api/auth.go   +42 -3 │
│2 ▲ fix-flaky-test        ││   the agent's own interface, running   ││A api/token.go     +18 │
│3 ✓ docs-pass             ││   in a real terminal                   ││M go.mod            +1 │
╰──────────────────────────╯│                                        ││                       │
╭─ Files · app ────────────╮│                                        │╰───────────────────────╯
│▾ api/                   •││                                        │╭─ Queue (1) ───────────╮
│   auth.go               M││                                        ││▸ then run the linter  │
╰──────────────────────────╯╰────────────────────────────────────────╯╰───────────────────────╯
 ▲ fix-flaky-test needs input                   ctx 82k · in 4.2M · out 38k · ctrl+a ? help
```

Website and full documentation: <https://henomis.github.io/cicoria/>

## What it does

- **Runs any agent, untouched.** Each agent is its own command-line program in a real terminal pane, so nothing of its interface is lost.
- **Shows a status for every session**: working, needs input, or finished while you were elsewhere. One key jumps to the next session that needs you, and a desktop notification tells you when one starts waiting.
- **Keeps the changes of a session reviewable**, counted from the commit it started on, so work the agent has already committed is still there. Diffs open full screen, with search and hunk navigation.
- **Lets you write notes on lines of code** and send them to the agent as one message.
- **Queues messages** and sends each one when the agent ends its turn.
- **Counts tokens** for Claude Code sessions.
- **Remembers** sessions, their directories, notes and queues between runs. Claude Code sessions continue their conversation.

Claude Code gets the full integration: its status comes from hooks, and the files it edits are highlighted. For any other agent, status is guessed from terminal activity.

## Install

```sh
go install github.com/henomis/cicoria/cmd/cicoria@latest
```

Requirements:

- Go 1.26 or later
- `git`
- the agents you want to run (`claude` by default)
- a Unix-like system; cicoria is developed and tested on Linux. Desktop notifications use `notify-send`.

## Quick start

```sh
cicoria            # workspace opened on the current directory
cicoria ~/src/app  # or on another one
```

Everything you type goes to the agent on screen. `ctrl+a` starts a cicoria command; `ctrl+a ?` lists them all.

| Key | Command |
|---|---|
| `c` | New session: pick the agent, its directory and a name |
| `1`-`9`, `n`, `p` | Go to a session |
| `a` | Go to the next session that needs input |
| `,` `r` `x` | Rename, restart, close the session |
| `s` `f` `g` | Focus the sessions, files, changes panel |
| `m` | Queue a message for the agent |
| `u` | Focus the queue panel |
| `d` | Review changes (full-screen diff) |
| `t` | Send review notes to the agent |
| `h` | Review from here: count changes from the last commit |
| `o` | Open a file by name |
| `b` | Show or hide the side panels |
| `[` `]` | Show or hide the left, right panels |
| `space` | Command palette |
| `?` | Help |
| `q` | Quit |
| `ctrl+a` | Send `ctrl+a` to the agent |

In a side panel, `j`/`k` move, `enter` opens, `tab` goes to the next panel and `esc` gives the keys back to the agent. The mouse works too.

## Sessions

A session is one agent working in one directory, so one cicoria can hold sessions from several projects. `ctrl+a c` asks for the agent, then the directory (`tab` completes, `ctrl+w` deletes a path component, `ctrl+u` clears, and `~` and relative paths work), then a name, offering one that is free.

Keep one session to a directory. The files and changes panels show the project of the session on screen, so two sessions in the same directory cannot be told apart by their changes; cicoria warns when you open a second one.

### Status

| Mark | Meaning |
|---|---|
| `●` | Working |
| `▲` | Needs input: the agent asks a question or a permission |
| `✓` | Finished its turn while you were looking elsewhere |
| `○` | Idle |
| `✕` | The agent's process has exited; `ctrl+a r` restarts it |

Each row also shows for how long the session has been in its status. A Claude Code session that says it is working but has given no sign of life for 30 seconds reads `working?`, as after an interrupted turn.

## Review

The changes of a session are everything that differs from the commit it started on, committed or not; the panel says how many commits were made since. In the changes panel, `s` switches to what is not committed and back. `ctrl+a h` starts the count again from the last commit, once you have looked.

Files and diffs open on the whole screen: `j`/`k` move a line, `space`/`b` a page, `/` searches, `[` `]` jump between hunks, `tab` goes to the next changed file, `d` switches between the file and its diff, `e` opens `$EDITOR`, `@` mentions the file to the agent, `q` goes back.

### Notes

A note is a remark on one or more lines of a file or diff. In the full-screen view, move to a line (or press `v` and move to select several) and press `c` to write one; `c` on a line that has a note rewrites it, `x` deletes it. Lines with a note carry a `*` in the margin.

Notes wait until you send them with `s` (or `ctrl+a t`): all together, as one message that names each file and its lines, quotes them and gives your text. You choose whether it goes to the agent at once or through the queue.

## Queue

While an agent works you can line up what to tell it next. `ctrl+a m` asks for a message; the line can be edited with the arrows, `ctrl+w`, `ctrl+u` and the like. Messages wait in a panel under the changes and go to the agent one by one, each when it ends its turn; an idle agent gets the first at once. Nothing is sent while the agent waits for a permission.

In the queue panel (`ctrl+a u`): `enter` sends the next message now, `p` pauses and resumes, `e` edits, `x` deletes, `J`/`K` reorder, `a` adds. Pause when a turn ends with a question you want to answer first.

Only Claude Code says when its turn ends. Other agents keep a queue too, but each message is sent when you press `enter` in the panel. Queues are remembered across restarts and come back paused.

## Tokens

For Claude Code sessions the status bar shows `ctx`, the size of the context, and `in` and `out`, what the model has read and written over the whole conversation, cached input included. With several sessions counting, `all` is what they add up to.

## Configure

`~/.config/cicoria/config.toml` (all optional):

```toml
prefix = "ctrl+a"
default_agent = "claude"
notify = true            # desktop notification when a background session needs input
left_panel = true        # false opens with sessions and files hidden; ctrl+a [ shows them
right_panel = true       # false opens with changes and the queue hidden; ctrl+a ] shows them
animations = true        # ASCII art when cicoria opens and quits, and a session is created or closed

# Any command line can be an agent. Its status is guessed from terminal
# activity: output means working, the bell means it needs input.
[agents.copilot]
command = ["copilot"]

# An entry named "claude" changes how Claude Code is started.
[agents.claude]
command = ["claude", "--model", "opus"]
```

A plain `shell` is always available as a session type.

With `animations` on, a second of ASCII art plays in the agent pane when cicoria opens and quits and when a session is created or closed. Any key ends it.

## What is remembered

Sessions are saved for the directory cicoria was opened on, under `$XDG_STATE_HOME/cicoria` (or `~/.local/state/cicoria`), and reopened the next time with their directories, notes and queues. Agents stop when cicoria closes; Claude Code sessions continue their conversation when reopened.

## Develop

```sh
make build      # build ./cicoria
make check      # formatting, vet and tests: run it before a commit
make test-real  # the test that embeds the real Claude Code
make site       # serve the website in docs/ on http://localhost:8000
make help       # every target
```

The website lives in `docs/` and is plain HTML, CSS and JavaScript, with no build step.

## The name

Cicoria is Italian for chicory. One grows in the agent pane each time you start.
