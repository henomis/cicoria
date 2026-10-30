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
	"os/exec"
	"strings"
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
	cfg := config.Config{Worktrees: []config.WorktreeConfig{{Repo: dir, Teardown: []string{"sh", "-c", "sleep 30; true"}}}}
	start := time.Now()
	kept := teardown(cfg, dir)
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("the teardown was stopped after %s", d)
	}
	if !strings.Contains(kept, "took more than") {
		t.Errorf("teardown = %q, want it kept for taking too long", kept)
	}
}
