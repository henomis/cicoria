# cicoria

A terminal workspace for the AI-first engineer: run several coding agents side by side, see which one needs you, and review what they changed, without leaving the terminal.

```
╭─ Sessions ───────────────╮╭─ auth-refactor · claude · working ─────╮╭─ Changes (3) ─────────╮
│1 ● auth-refactor         ││                                        ││M api/auth.go   +42 -3 │
│2 ▲ fix-flaky-test        ││   the agent's own interface, running   ││A api/token.go     +18 │
│3 ✓ docs-pass             ││   in a real terminal                   ││M go.mod            +1 │
╰──────────────────────────╯│                                        ││                       │
╭─ Files ──────────────────╮│                                        ││                       │
│▾ api/                   •││                                        ││                       │
│   auth.go               M││                                        ││                       │
╰──────────────────────────╯╰────────────────────────────────────────╯╰───────────────────────╯
 ▲ fix-flaky-test needs input                                          ctx 82k · ctrl+a ? help
```

Agents run as their own command-line program inside a pane, so nothing of their interface is lost and any agent works. Claude Code gets extra integration: its status (working, needs input, idle) comes from hooks, sessions are resumed across restarts, and files it edits are highlighted in the tree.

## Install

```sh
go install github.com/henomis/cicoria/cmd/cicoria@latest
```

Requires Go 1.26+, `git`, and the agents you want to run (`claude` by default).

## Use

```sh
cicoria            # workspace opened on the current directory
cicoria ~/src/app  # or on another one
```

Everything you type goes to the agent on screen. `ctrl+a` starts a command:

| Key | Command |
|---|---|
| `c` | New session |
| `1`-`9`, `n`, `p` | Go to a session |
| `a` | Go to the next session that needs input |
| `,` `r` `x` | Rename, restart, close the session |
| `s` `f` `g` | Focus the sessions, files, changes panel |
| `m` | Queue a message for the agent |
| `u` | Focus the queue panel |
| `d` | Review changes (full-screen diff) |
| `o` | Open a file by name |
| `b` | Show or hide the side panels |
| `[` `]` | Show or hide the left, right panels |
| `space` | Command palette |
| `?` | Help |
| `q` | Quit |
| `ctrl+a` | Send `ctrl+a` to the agent |

Files and diffs open on the whole screen: `j`/`k`/`space`/`b` scroll, `/` searches, `[` `]` jump between hunks, `tab` goes to the next changed file, `d` switches between the file and its diff, `e` opens `$EDITOR`, `@` mentions the file to the agent, `q` goes back.

### Queue

While an agent works you can line up what to tell it next. `ctrl+a m` asks for messages, one per line until an empty line or `esc`. They wait in a queue, shown under the changes, and go to the agent one by one, each when it ends its turn; an idle agent gets the first at once. Nothing is sent while the agent waits for a permission.

In the queue panel (`ctrl+a u`): `enter` sends the next message now, `p` pauses and resumes, `e` edits, `x` deletes, `J`/`K` reorder, `a` adds. Pause when a turn ends with a question you want to answer first.

Only Claude Code says when its turn ends. Other agents keep a queue too, but each message is sent when you press `enter` in the panel. Queues are remembered across restarts and come back paused.

Each session has its own working directory, so one cicoria can hold sessions from several projects. A new session asks for the agent and then for the directory, offering the one of the session on screen: `tab` completes, `ctrl+w` deletes a path component, `ctrl+u` clears, and `~` and relative paths work. The file and change panels always show the project of the session on screen.

Sessions, with their directories, are remembered for the directory cicoria was opened on and reopened the next time. Agents stop when cicoria closes; Claude Code sessions continue their conversation when reopened.

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

With `animations` on, a short piece of ASCII art plays in the agent pane when cicoria opens and quits and when a session is created or closed. Any key ends it.

## Develop

```sh
go test ./...
CICORIA_E2E_REAL=$PWD go test -run TestRealAgent -v ./cmd/cicoria   # embeds the real Claude Code
```
