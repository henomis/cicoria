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

import "testing"

func TestIsWSL(t *testing.T) {
	env := func(vars map[string]string) func(string) string {
		return func(name string) string { return vars[name] }
	}
	for _, c := range []struct {
		name    string
		vars    map[string]string
		release string
		want    bool
	}{
		{"linux", nil, "6.12.111+deb13-amd64", false},
		{"the name of the distribution", map[string]string{"WSL_DISTRO_NAME": "Ubuntu"}, "", true},
		{"the interop socket", map[string]string{"WSL_INTEROP": "/run/WSL/8_interop"}, "", true},
		{"the kernel of WSL2", nil, "5.15.167.4-microsoft-standard-WSL2\n", true},
		{"the kernel of WSL1", nil, "4.4.0-19041-Microsoft", true},
	} {
		if got := isWSL(env(c.vars), c.release); got != c.want {
			t.Errorf("%s: isWSL is %v", c.name, got)
		}
	}
}

func TestOpener(t *testing.T) {
	for system, want := range map[string]string{
		"linux":   "xdg-open",
		"freebsd": "xdg-open",
		"darwin":  "open",
		"wsl":     "explorer.exe",
	} {
		if got := opener(system); got != want {
			t.Errorf("%s opens a file with %q, not %q", system, got, want)
		}
	}
}
