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
	"net"
	"testing"

	"github.com/henomis/cicoria/internal/config"
	"github.com/henomis/cicoria/internal/session"
)

// A range is skipped when a session has it, or a program listens in it.
func TestFreePorts(t *testing.T) {
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	busy := l.Addr().(*net.TCPAddr).Port
	// The range of 3 ports from base holds the one in use, as its last.
	base := busy - 5
	m := &Model{cfg: config.Config{PortBase: base, PortsPerSession: 3}}
	m.sessions = []*session.Session{{Port: base}}
	if got := m.freePorts(); got != base+6 {
		t.Errorf("free ports from %d = %d, want %d", base, got, base+6)
	}
	m.cfg.PortBase = 0
	if got := m.freePorts(); got != 0 {
		t.Errorf("free ports with none given = %d", got)
	}
}

// Ports saved for a session are its own while the settings give that range
// and no other session has it.
func TestOwnPorts(t *testing.T) {
	m := &Model{cfg: config.Config{PortBase: 20000, PortsPerSession: 10}}
	m.sessions = []*session.Session{{Port: 20010}}
	for port, want := range map[int]bool{20000: true, 20020: true, 20010: false, 20005: false, 19990: false, 0: false} {
		if got := m.ownPorts(port); got != want {
			t.Errorf("own ports from %d = %v, want %v", port, got, want)
		}
	}
	m.cfg.PortBase = 0
	if m.ownPorts(20000) {
		t.Error("ports kept with none given")
	}
}
