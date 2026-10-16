// Copyright 2026 Simone Vellei
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Command cicoria is a terminal workspace for supervising coding agents.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/henomis/cicoria/internal/app"
	"github.com/henomis/cicoria/internal/config"
	"github.com/henomis/cicoria/internal/ipc"
)

const usage = `cicoria — a terminal workspace for supervising coding agents

Usage:
  cicoria [flags] [directory]   open the workspace of a project (default: .)
  cicoria hook [name]           report an agent hook event (used by agents)

Flags:
`

func main() {
	if len(os.Args) > 1 && os.Args[1] == "hook" {
		// A hook must never get in the agent's way: report what can be
		// reported and always succeed.
		name := ""
		if len(os.Args) > 2 {
			name = os.Args[2]
		}
		_ = ipc.Forward(name, os.Stdin)
		return
	}

	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "cicoria:", err)
		os.Exit(1)
	}
}

// released is the version of a released binary, set when it is built.
var released string

// version returns the version cicoria was built from: the tag of a release,
// or what the Go toolchain recorded, a tag when installed with "go install"
// and a commit otherwise.
func version() string {
	if released != "" {
		return released
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "(unknown)"
}

// pushTitle and popTitle save the terminal's window and icon titles and
// put them back, as xterm defines them; a terminal without the stack
// ignores both.
const (
	pushTitle = "\x1b[22;0t"
	popTitle  = "\x1b[23;0t"
)

func run(args []string) error {
	flags := flag.NewFlagSet("cicoria", flag.ExitOnError)
	configPath := flags.String("config", config.Path(), "path of the config file")
	showVersion := flags.Bool("version", false, "print the version and exit")
	flags.Usage = func() {
		_, _ = fmt.Fprint(flags.Output(), usage)
		flags.PrintDefaults()
	}
	_ = flags.Parse(args)
	if *showVersion {
		fmt.Println("cicoria", version())
		return nil
	}

	dir := "."
	switch flags.NArg() {
	case 0:
	case 1:
		dir = flags.Arg(0)
	default:
		return errors.New("too many arguments")
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	// Agents see the real path, so use the same one to match what they report.
	if dir, err = filepath.EvalSymlinks(dir); err != nil {
		return err
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}

	dark := lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
	// One workspace after the other, for as long as the user asks for another.
	// A workspace picked from inside cicoria was asked about there, when
	// another cicoria has it open.
	// The title cicoria sets is taken back when it quits: the terminal's own
	// is saved here and restored, where the terminal keeps a stack of them.
	fmt.Print(pushTitle)
	defer fmt.Print(popTitle)
	for agreed := false; dir != ""; agreed = true {
		m, err := app.New(cfg, dir, dark, agreed)
		if err != nil {
			return err
		}
		_, err = tea.NewProgram(m).Run()
		if err = errors.Join(err, m.Close()); err != nil {
			return err
		}
		dir = m.Next()
	}
	return nil
}
