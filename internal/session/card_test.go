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

package session

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/henomis/cicoria/internal/agent"
	"github.com/henomis/cicoria/internal/term"
)

func TestCardsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	want := State{Dir: "/w", Sessions: []Saved{{ID: "1", Name: "a", Agent: "claude"}}, Cards: []Card{{ID: "c1", Title: "t", Prompt: "p", Dir: "/w/app"}}}
	if err := SaveState(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadState(path)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, %v; want %+v", got, err, want)
	}
}

func TestStateWithoutCards(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	// As it was saved before there was a backlog.
	if err := os.WriteFile(path, []byte(`{"dir":"/w","sessions":[{"id":"1","name":"a","agent":"claude"}],"active":0}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := LoadState(path)
	if err != nil || len(st.Sessions) != 1 || len(st.Cards) != 0 {
		t.Fatalf("got %+v, %v", st, err)
	}
}

func TestCardText(t *testing.T) {
	for _, c := range []Card{
		{Title: "fix the login", Prompt: "the form sends the password twice\n\nlook at auth.go"},
		{Title: "one line", Prompt: "one line"},
	} {
		title, prompt, ok := ParseCard(CardText(c))
		if !ok || title != c.Title || prompt != c.Prompt {
			t.Errorf("%+v came back as %q, %q, %v", c, title, prompt, ok)
		}
	}
	// A new card starts with nothing but the help, which is not read.
	if _, _, ok := ParseCard(CardText(Card{})); ok {
		t.Error("an empty card was read as one")
	}
	// What follows the scissors is ignored, even in the middle of the text.
	title, prompt, ok := ParseCard("  title  \n\n# a heading the agent gets\nbody\n" + cardCut + "\nnot this\n")
	if !ok || title != "title" || prompt != "# a heading the agent gets\nbody" {
		t.Errorf("got %q, %q, %v", title, prompt, ok)
	}
}

func TestLane(t *testing.T) {
	running := &term.Terminal{}
	for _, tc := range []struct {
		name    string
		s       Session
		changed bool
		want    Lane
	}{
		{"asks", Session{Term: running, Status: agent.NeedsInput}, false, LaneNeedsYou},
		{"could not start", Session{Err: errors.New("no such agent"), Status: agent.Exited}, false, LaneNeedsYou},
		{"starting", Session{Term: running, Status: agent.Starting}, false, LaneWorking},
		{"working", Session{Term: running, Status: agent.Working}, true, LaneWorking},
		{"finished while away", Session{Term: running, Status: agent.Idle, Unseen: true}, false, LaneReview},
		{"idle with changes", Session{Term: running, Status: agent.Idle}, true, LaneReview},
		{"idle with notes", Session{Term: running, Status: agent.Idle, Notes: []Note{{Path: "x"}}}, false, LaneReview},
		{"idle", Session{Term: running, Status: agent.Idle}, false, LaneSettled},
		{"exited", Session{Term: running, Status: agent.Exited}, true, LaneSettled},
	} {
		if got := tc.s.Lane(tc.changed); got != tc.want {
			t.Errorf("%s: lane %d, want %d", tc.name, got, tc.want)
		}
	}
}
