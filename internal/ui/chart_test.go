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

package ui

import "testing"

func TestHeat(t *testing.T) {
	for _, c := range []struct {
		n, most int
		want    string
	}{{0, 4, "·"}, {1, 4, "░"}, {2, 4, "▒"}, {3, 4, "▓"}, {4, 4, "█"}, {1, 100, "░"}, {5, 0, "·"}} {
		if got := Heat(c.n, c.most); got != c.want {
			t.Errorf("Heat(%d, %d) = %q, want %q", c.n, c.most, got, c.want)
		}
	}
}
