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
	"runtime"
	"strings"
)

// system names the system cicoria runs on: runtime.GOOS, or "wsl" for the
// Linux that Windows runs, where the desktop is the one of Windows.
func system() string {
	if runtime.GOOS == "linux" {
		release, _ := os.ReadFile("/proc/sys/kernel/osrelease")
		if isWSL(os.Getenv, string(release)) {
			return "wsl"
		}
	}
	return runtime.GOOS
}

// isWSL reports whether a Linux with this environment and kernel release is
// the Windows Subsystem for Linux.
func isWSL(getenv func(string) string, release string) bool {
	return getenv("WSL_DISTRO_NAME") != "" || getenv("WSL_INTEROP") != "" ||
		strings.Contains(strings.ToLower(release), "microsoft")
}

// opener returns the program that opens a file with the one the system has
// for it.
func opener(system string) string {
	switch system {
	case "darwin":
		return "open"
	case "wsl":
		return "explorer.exe"
	}
	return "xdg-open"
}
