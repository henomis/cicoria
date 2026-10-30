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
	"slices"
	"strconv"

	"github.com/henomis/cicoria/internal/session"
)

// freePorts returns the first port of a range the settings give sessions
// that no other session has and no program listens in, or 0 when there is
// none or the settings give sessions no ports.
func (m *Model) freePorts() int {
	if m.cfg.PortBase == 0 {
		return 0
	}
	n := m.cfg.PortsPerSession
	for p := m.cfg.PortBase; p+n-1 <= 65535; p += n {
		if !slices.ContainsFunc(m.sessions, func(s *session.Session) bool { return s.Port == p }) && portsFree(p, n) {
			return p
		}
	}
	return 0
}

// ownPorts reports whether the range of a session saved with port as its
// first is one the settings give, and no session in the list has.
func (m *Model) ownPorts(port int) bool {
	base, n := m.cfg.PortBase, m.cfg.PortsPerSession
	return base != 0 && port >= base && (port-base)%n == 0 && port+n-1 <= 65535 &&
		!slices.ContainsFunc(m.sessions, func(s *session.Session) bool { return s.Port == port })
}

// portsFree reports whether n ports from first can be listened on: whether
// no program listens on them yet.
func portsFree(first, n int) bool {
	for p := first; p < first+n; p++ {
		l, err := net.Listen("tcp", ":"+strconv.Itoa(p))
		if err != nil {
			return false
		}
		_ = l.Close()
	}
	return true
}
