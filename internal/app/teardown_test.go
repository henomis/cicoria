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

package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/henomis/cicoria/internal/config"
)

// A teardown that hangs is stopped on time, with the programs it started.
func TestTeardownTimeout(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	defer func(d time.Duration) { teardownTimeout = d }(teardownTimeout)
	teardownTimeout = 200 * time.Millisecond
	// The sleep is a program the shell starts, and holds its output open.
	pidFile := filepath.Join(t.TempDir(), "pid")
	script := "sleep 30 & echo $! > " + pidFile + "; wait"
	cfg := config.Config{Worktrees: []config.WorktreeConfig{{Repo: dir, Teardown: []string{"sh", "-c", script}}}}
	start := time.Now()
	kept := teardown(cfg, dir)
	// Waiting for the output to close would take the WaitDelay of a second
	// more: stopping the whole group closes it at once.
	if d := time.Since(start); d >= time.Second {
		t.Errorf("the teardown was stopped after %s", d)
	}
	if !strings.Contains(kept, "took more than") {
		t.Errorf("teardown = %q, want it kept for taking too long", kept)
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	// The sleep is gone once whatever adopted it has reaped it.
	for deadline := time.Now().Add(2 * time.Second); syscall.Kill(pid, 0) == nil; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("the sleep the teardown started, %d, is still running", pid)
		}
	}
}
