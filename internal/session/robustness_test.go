package session

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/jskoll/wyrm/internal/config"
	"github.com/jskoll/wyrm/internal/tmux"
)

// noisyRunner prepends a line of diagnostic output to new-session's response,
// reproducing what a user with one bad line in ~/.tmux.conf used to get:
// new-session is the command that starts the tmux server, so the server's
// config parse errors land on stderr at exactly the moment wyrm is parsing
// that command's "-F" output positionally.
type noisyRunner struct {
	calls  [][]string
	notice string
}

func (n *noisyRunner) Run(args ...string) (string, error) {
	n.calls = append(n.calls, args)
	switch args[0] {
	case "new-session":
		return n.notice + "\n$1|proj|@1|%1", nil
	case "split-window":
		return n.notice + "\n%2", nil
	case "list-sessions":
		// tmux reports "no server running" on stderr and exits non-zero; Exec
		// surfaces that text as the output, which is what NoServerRunning reads.
		return "no server running on /tmp/tmux-0/default", errors.New("exit status 1")
	}
	return "", nil
}

func (n *noisyRunner) ran(prefix string) bool {
	for _, c := range n.calls {
		if strings.HasPrefix(strings.Join(c, " "), prefix) {
			return true
		}
	}
	return false
}

// TestCreateRejectsContaminatedSessionID is the regression test for the
// worst-case version of that bug: the noise still leaves three "|" separators,
// so the field-count check passed and wyrm went on to target every later
// command at a session ID with a config error glued to the front of it.
func TestCreateRejectsContaminatedSessionID(t *testing.T) {
	r := &noisyRunner{notice: "/home/u/.tmux.conf:12: unknown command: fooo"}
	cfg := &config.Config{
		Session: config.Session{Name: "proj", Root: "/tmp/proj"},
		Windows: []config.Window{{Name: "a"}, {Name: "b"}},
	}

	var stderr bytes.Buffer
	_, _, _, err := Create(r, cfg, &bytes.Buffer{}, &stderr)
	if err == nil {
		t.Fatal("Create = nil error, want a rejected session ID")
	}
	if !strings.Contains(err.Error(), "session id") {
		t.Errorf("error = %v, want it to name the malformed id", err)
	}
	if r.ran("new-window") {
		t.Error("new-window was issued against an unvalidated session id")
	}
}

func TestSplitPaneRejectsContaminatedPaneID(t *testing.T) {
	if _, err := splitPane(&noisyRunner{notice: "warning"}, "%1", config.Split{Type: "h"}, "", nil); err == nil {
		t.Fatal("splitPane = nil error, want a rejected pane id")
	}
}

func TestValidIDShapes(t *testing.T) {
	cases := []struct {
		sigil byte
		s     string
		want  bool
	}{
		{tmux.SessionSigil, "$1", true},
		{tmux.SessionSigil, "$42", true},
		{tmux.SessionSigil, "$", false},
		{tmux.SessionSigil, "@1", false},
		{tmux.SessionSigil, "warning\n$1", false},
		{tmux.SessionSigil, "$1x", false},
		{tmux.WindowSigil, "@0", true},
		{tmux.PaneSigil, "%7", true},
		{tmux.PaneSigil, "", false},
	}
	for _, c := range cases {
		if got := tmux.ValidID(c.sigil, c.s); got != c.want {
			t.Errorf("ValidID(%q, %q) = %v, want %v", string(c.sigil), c.s, got, c.want)
		}
	}
}

// TestCreateSelectsFirstWindowByDefault pins the documented default. Every
// window used to be created without -d, so tmux made each new one current and
// the session opened on the *last* window in the config while the README
// promised the first.
func TestCreateSelectsFirstWindowByDefault(t *testing.T) {
	r := &fakeRunner{}
	cfg := &config.Config{
		Session: config.Session{Name: "proj", Root: "/tmp/proj"},
		Windows: []config.Window{{Name: "first"}, {Name: "second"}, {Name: "third"}},
	}
	if _, _, _, err := Create(r, cfg, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	joined := r.joined()
	for _, c := range joined {
		if strings.HasPrefix(c, "new-window") && !strings.Contains(c, " -d ") {
			t.Errorf("new-window issued without -d: %q", c)
		}
	}
	if last := joined[len(joined)-1]; last != "select-window -t @1" {
		t.Errorf("last call = %q, want the first window selected", last)
	}
}

// TestCreateReportsNameTmuxActuallyUsed covers the tmux builds that rewrite
// "." and ":" in session names to "_". Reporting the config's name instead of
// the real one made the *next* run fail to find the session, try to create a
// duplicate, and error out — and made `wyrm kill` permanently unable to find
// it.
func TestCreateReportsNameTmuxActuallyUsed(t *testing.T) {
	r := &fakeRunner{sessionName: "example_com"}
	cfg := &config.Config{
		Session: config.Session{Name: "example.com", Root: "/tmp/proj"},
		Windows: []config.Window{{Name: "w"}},
	}
	var stderr bytes.Buffer
	name, _, _, err := Create(r, cfg, &bytes.Buffer{}, &stderr)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if name != "example_com" {
		t.Errorf("name = %q, want the name tmux actually assigned (example_com)", name)
	}
	if !strings.Contains(stderr.String(), "example_com") {
		t.Errorf("stderr = %q, want a warning that the name was rewritten", stderr.String())
	}
}

// TestPreWindowReachesOrphanedBasePane covers the half of the pre_window bug
// where a pane was skipped: a first entry with a type splits the window's
// initial pane and lands its command in the new one, leaving the initial pane
// a bare shell that never ran pre_window — though the docs promise it runs in
// every pane.
func TestPreWindowReachesOrphanedBasePane(t *testing.T) {
	r := &fakeRunner{}
	cfg := &config.Config{
		Session: config.Session{Name: "proj", Root: "/tmp/proj"},
		Windows: []config.Window{{
			Name:      "w",
			PreWindow: "setup",
			Splits: []config.Split{
				{Type: "h", Size: 50, Command: "nvim"},
			},
		}},
	}
	if _, _, _, err := Create(r, cfg, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	joined := strings.Join(r.joined(), "\n")
	for _, pane := range []string{"%1", "%2"} {
		if !strings.Contains(joined, "send-keys -t "+pane+" -l -- setup") {
			t.Errorf("pre_window never reached pane %s:\n%s", pane, joined)
		}
	}
}

// TestPreWindowSentOncePerPane covers the other half: pre_window was emitted
// per split-tree entry, and a nested container reuses its parent's pane as its
// own first entry — so a two-level nest typed it into that pane twice. Fine
// for "nvm use", not for "cd subdir" or anything appending to PATH.
func TestPreWindowSentOncePerPane(t *testing.T) {
	r := &fakeRunner{}
	cfg := &config.Config{
		Session: config.Session{Name: "proj", Root: "/tmp/proj"},
		Windows: []config.Window{{
			Name:      "w",
			PreWindow: "setup",
			Splits: []config.Split{
				{Command: "a", Children: []config.Split{
					{Command: "b"},
					{Type: "v", Size: 50, Command: "c"},
				}},
			},
		}},
	}
	if _, _, _, err := Create(r, cfg, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if n := strings.Count(strings.Join(r.joined(), "\n"), "send-keys -t %1 -l -- setup"); n != 1 {
		t.Errorf("pre_window sent to %%1 %d times, want exactly 1:\n%s", n, strings.Join(r.joined(), "\n"))
	}
}

// TestSendKeysUsesLiteralFlag guards commands that collide with tmux key
// names. Without "-l --", `command = "up"` pressed the Up arrow and Enter,
// re-running the previous shell history entry instead of typing "up".
func TestSendKeysUsesLiteralFlag(t *testing.T) {
	r := &fakeRunner{}
	cfg := &config.Config{
		Session: config.Session{Name: "proj", Root: "/tmp/proj"},
		Windows: []config.Window{{Name: "w", Splits: []config.Split{{Command: "up"}}}},
	}
	if _, _, _, err := Create(r, cfg, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	joined := strings.Join(r.joined(), "\n")
	if !strings.Contains(joined, "send-keys -t %1 -l -- up") {
		t.Errorf("command not sent literally:\n%s", joined)
	}
	if !strings.Contains(joined, "send-keys -t %1 Enter") {
		t.Errorf("Enter not sent as a key:\n%s", joined)
	}
}

// TestCreateRollsBackHalfBuiltSession: a build that fails partway used to
// leave the session running, so the next `wyrm` found it, said "already
// running, attaching", and handed over a session missing most of its windows
// with no sign anything had gone wrong.
func TestCreateRollsBackHalfBuiltSession(t *testing.T) {
	r := &fakeRunner{fail: map[string]bool{"new-window": true}}
	cfg := &config.Config{
		Session: config.Session{Name: "proj", Root: "/tmp/proj"},
		Windows: []config.Window{{Name: "first"}, {Name: "second"}},
	}
	if _, _, _, err := Create(r, cfg, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("Create = nil error, want the new-window failure")
	}
	if !strings.Contains(strings.Join(r.joined(), "\n"), "kill-session -t $1") {
		t.Errorf("half-built session was not cleaned up:\n%s", strings.Join(r.joined(), "\n"))
	}
}

// TestHookOutputIsStreamed: hook output used to be captured and discarded
// unless the hook failed, so a slow `git pull && npm install` blocked wyrm
// with a blank screen.
func TestHookOutputIsStreamed(t *testing.T) {
	var stderr bytes.Buffer
	if err := runHook(options{}, "echo hello-from-hook", t.TempDir(), "on_project_start", &stderr); err != nil {
		t.Fatalf("runHook: %v", err)
	}
	if !strings.Contains(stderr.String(), "hello-from-hook") {
		t.Errorf("stderr = %q, want the hook's own output", stderr.String())
	}
	if !strings.Contains(stderr.String(), "running on_project_start") {
		t.Errorf("stderr = %q, want the hook announced before it runs", stderr.String())
	}
}
