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

package web

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// bearer is a client that sends the token of the link.
type bearer struct {
	token string
	http.Client
}

func (b *bearer) Do(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", "Bearer "+b.token)
	return b.Client.Do(req)
}

func (b *bearer) Get(url string) (*http.Response, error) {
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	return b.Do(req)
}

func (b *bearer) Post(url, contentType string, body io.Reader) (*http.Response, error) {
	req, _ := http.NewRequest(http.MethodPost, url, body)
	req.Header.Set("Content-Type", contentType)
	return b.Do(req)
}

// start runs a server and returns it with a client that has its token.
func start(t *testing.T) (*Server, *bearer, string) {
	t.Helper()
	s, err := Start("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	u, _ := url.Parse(s.URL())
	return s, &bearer{token: u.Query().Get("t"), Client: http.Client{Timeout: 5 * time.Second}}, "http://" + u.Host
}

func TestTokenIsNeeded(t *testing.T) {
	s, c, base := start(t)
	if !s.Local() {
		t.Error("127.0.0.1 is not local")
	}
	// The page is no secret: what it shows is.
	for path, want := range map[string]int{
		"/":                       http.StatusOK,
		"/app.js":                 http.StatusOK,
		"/events":                 http.StatusUnauthorized,
		"/events?t=wrong":         http.StatusUnauthorized,
		"/api/sessions/a/changes": http.StatusUnauthorized,
	} {
		r, err := http.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = r.Body.Close()
		if r.StatusCode != want {
			t.Errorf("%s without the token: %s", path, r.Status)
		}
	}
	// The stream cannot send a header: it has the token in the address.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/events?t="+c.token, nil)
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Body.Close()
	if r.StatusCode != http.StatusOK {
		t.Errorf("/events with the token: %s", r.Status)
	}
}

// TestEvents streams a snapshot, then the next one.
func TestEvents(t *testing.T) {
	s, c, base := start(t)
	s.Publish(Snapshot{Workspace: "~/p", Sessions: []Session{{ID: "a", Name: "claude-1", Status: "idle"}}})

	r, err := c.Get(base + "/events?watch=a")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Body.Close() }()
	lines := bufio.NewScanner(r.Body)
	next := func() string {
		for lines.Scan() {
			if data, ok := strings.CutPrefix(lines.Text(), "data: "); ok {
				return data
			}
		}
		t.Fatal("the stream ended")
		return ""
	}
	if got := next(); !strings.Contains(got, `"name":"claude-1"`) || !strings.Contains(got, `"status":"idle"`) {
		t.Errorf("first event = %s", got)
	}
	if got := s.Watching(); !slices.Equal(got, []string{"a"}) {
		t.Errorf("watching = %v", got)
	}
	s.Publish(Snapshot{Workspace: "~/p", Sessions: []Session{{ID: "a", Name: "claude-1", Status: "working"}}})
	if got := next(); !strings.Contains(got, `"status":"working"`) {
		t.Errorf("second event = %s", got)
	}
}

func TestActions(t *testing.T) {
	s, c, base := start(t)
	s.Publish(Snapshot{Sessions: []Session{
		{ID: "a", Running: true, Control: true},
		{ID: "b", Control: true},
		{ID: "w", Running: true},
	}})

	post := func(path, body string) (int, string) {
		t.Helper()
		r, err := c.Post(base+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = r.Body.Close() }()
		text, _ := io.ReadAll(r.Body)
		return r.StatusCode, strings.TrimSpace(string(text))
	}
	// answer plays the workspace: it takes the next action and says what
	// came of it.
	answer := func(want Action, err error) {
		t.Helper()
		select {
		case a := <-s.Actions():
			a.Reply(err)
			a.Done = nil
			if a != want {
				t.Errorf("action = %+v, want %+v", a, want)
			}
		case <-time.After(time.Second):
			t.Error("no action came")
		}
	}

	// What the workspace answers is what the page is told.
	go answer(Action{Session: "a", Kind: ActQueue, Text: "run the tests"}, nil)
	if code, _ := post("/api/sessions/a/queue", `{"text":"run the tests"}`); code != http.StatusNoContent {
		t.Errorf("queue: %d", code)
	}
	go answer(Action{Session: "a", Kind: ActChoose, Text: "Yes", Index: 0}, errors.New("the question changed"))
	if code, why := post("/api/sessions/a/choose", `{"text":"Yes","index":0}`); code != http.StatusConflict || why != "the question changed" {
		t.Errorf("choose: %d %q", code, why)
	}
	go answer(Action{Session: "a", Kind: ActInterrupt}, nil)
	if code, _ := post("/api/sessions/a/interrupt", ``); code != http.StatusNoContent {
		t.Errorf("interrupt: %d", code)
	}

	for path, want := range map[string]int{
		"/api/sessions/c/send":  http.StatusNotFound,
		"/api/sessions/a/close": http.StatusNotFound,
		"/api/sessions/a/key":   http.StatusNotFound,
		// An agent that is not running is only queued for.
		"/api/sessions/b/send":      http.StatusConflict,
		"/api/sessions/b/choose":    http.StatusConflict,
		"/api/sessions/b/interrupt": http.StatusConflict,
		// A session shared to watch is only queued for.
		"/api/sessions/w/send":      http.StatusForbidden,
		"/api/sessions/w/choose":    http.StatusForbidden,
		"/api/sessions/w/interrupt": http.StatusForbidden,
	} {
		if code, _ := post(path, `{"text":"y"}`); code != want {
			t.Errorf("%s: %d, want %d", path, code, want)
		}
	}
	if code, _ := post("/api/sessions/a/send", `{}`); code != http.StatusBadRequest {
		t.Errorf("nothing to send: %d", code)
	}

	// Watching still lets a queued message be taken back.
	go answer(Action{Session: "w", Kind: ActUnqueue, Text: "old", Index: 1}, nil)
	if code, _ := post("/api/sessions/w/unqueue", `{"text":"old","index":1}`); code != http.StatusNoContent {
		t.Errorf("unqueue: %d", code)
	}
}

// TestDiff shows the changes of a shared session, and nothing outside them.
func TestDiff(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "new.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s, c, base := start(t)
	s.Publish(Snapshot{Sessions: []Session{{ID: "a", Dir: dir}}})
	get := func(path string) (int, string) {
		t.Helper()
		r, err := c.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = r.Body.Close() }()
		body, _ := io.ReadAll(r.Body)
		return r.StatusCode, string(body)
	}
	if code, body := get("/api/sessions/a/changes"); code != http.StatusOK || !strings.Contains(body, `"path":"new.go"`) {
		t.Errorf("changes: %d %s", code, body)
	}
	if code, body := get("/api/sessions/a/diff?path=new.go"); code != http.StatusOK || !strings.Contains(body, "+package main") {
		t.Errorf("diff: %d %s", code, body)
	}
	if code, _ := get("/api/sessions/a/diff?path=" + url.QueryEscape("/etc/passwd")); code != http.StatusNotFound {
		t.Errorf("a file that is not a change: %d", code)
	}
	if code, _ := get("/api/sessions/b/changes"); code != http.StatusNotFound {
		t.Errorf("a session that is not shared: %d", code)
	}
}

// TestEveryAddress gives a link a phone can follow, not the address listened on.
func TestEveryAddress(t *testing.T) {
	s, err := Start("0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	u, err := url.Parse(s.URL())
	if err != nil {
		t.Fatal(err)
	}
	if u.Hostname() == "0.0.0.0" || u.Port() == "0" {
		t.Errorf("link = %s", s.URL())
	}
	t.Log(s.URL())
}
