// Command cicoria is a terminal workspace for supervising coding agents.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/henomis/cicoria/internal/app"
	"github.com/henomis/cicoria/internal/config"
	"github.com/henomis/cicoria/internal/ipc"
)

const usage = `cicoria — a terminal workspace for supervising coding agents

Usage:
  cicoria [flags] [directory]   open the workspace of a project (default: .)
  cicoria hook                  report an agent hook event (used by agents)

Flags:
`

func main() {
	if len(os.Args) > 1 && os.Args[1] == "hook" {
		// A hook must never get in the agent's way: report what can be
		// reported and always succeed.
		_ = ipc.Forward(os.Stdin)
		return
	}

	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "cicoria:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("cicoria", flag.ExitOnError)
	configPath := flags.String("config", config.Path(), "path of the config file")
	flags.Usage = func() {
		_, _ = fmt.Fprint(flags.Output(), usage)
		flags.PrintDefaults()
	}
	_ = flags.Parse(args)

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
	m, err := app.New(cfg, dir, dark)
	if err != nil {
		return err
	}
	_, err = tea.NewProgram(m).Run()
	return errors.Join(err, m.Close())
}
