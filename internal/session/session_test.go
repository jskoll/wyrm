package session

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/jskoll/wyrm/internal/config"
)

// fakeRunner records every tmux invocation and fabricates the outputs the
// real tmux would print for -P -F commands, handing out sequential window
// (@N) and pane (%N) IDs. "list-sessions" (used by tmux.FindSessionID to
// check whether a session is already running) returns listOutput verbatim,
// in the "id|name" format FindSessionID expects; empty means "not running".
type fakeRunner struct {
	calls   [][]string
	winSeq  int
	paneSeq int

	// sessionName is what new-session reports back as #{session_name}. Empty
	// means "whatever was asked for", i.e. the -s argument — the normal case.
	// Setting it simulates the tmux builds that rewrite "." and ":" to "_".
	sessionName string

	// fail forces the named command (args[0]) to return an error.
	fail map[string]bool
	// badNewSessionOutput makes new-session/new-window return output with
	// no "|" separator, exercising the "unexpected tmux output" path.
	badNewSessionOutput bool
	// listOutput is returned verbatim for "list-sessions" calls.
	listOutput string
	// listWindowsOutput backs "list-windows" (selectStartup's window lookup),
	// in the id-ordered "index|@id|active|layout|name" format ListWindows parses.
	listWindowsOutput string
	// listPanesOutput backs "list-panes", keyed by the -t target (a window ID
	// such as "@2"), in the "%id|index|active|command" format ListPanes parses.
	listPanesOutput map[string]string
}

func (f *fakeRunner) Run(args ...string) (string, error) {
	f.calls = append(f.calls, args)
	if f.fail[args[0]] {
		return "boom", errors.New("exit status 1")
	}
	switch args[0] {
	case "new-session":
		f.winSeq++
		f.paneSeq++
		if f.badNewSessionOutput {
			return "malformed", nil
		}
		name := f.sessionName
		if name == "" {
			for i, a := range args {
				if a == "-s" && i+1 < len(args) {
					name = args[i+1]
				}
			}
		}
		return fmt.Sprintf("$1|%s|@%d|%%%d", name, f.winSeq, f.paneSeq), nil
	case "new-window":
		f.winSeq++
		f.paneSeq++
		if f.badNewSessionOutput {
			return "malformed", nil
		}
		return fmt.Sprintf("@%d|%%%d", f.winSeq, f.paneSeq), nil
	case "split-window":
		f.paneSeq++
		return fmt.Sprintf("%%%d", f.paneSeq), nil
	case "list-sessions":
		return f.listOutput, nil
	case "list-windows":
		return f.listWindowsOutput, nil
	case "list-panes":
		return f.listPanesOutput[args[2]], nil
	}
	return "", nil
}

// joined flattens recorded calls for order-sensitive assertions.
func (f *fakeRunner) joined() []string {
	out := make([]string, len(f.calls))
	for i, c := range f.calls {
		out[i] = strings.Join(c, " ")
	}
	return out
}

func TestCreateSplitTree(t *testing.T) {
	cfg := &config.Config{
		Session: config.Session{Name: "proj", Root: "/tmp/proj"},
		Windows: []config.Window{{
			Name:      "editor",
			PreWindow: "nvm use 18",
			Splits: []config.Split{
				{Command: "nvim"},
				{Type: "h", Size: 30, Command: "npm run dev", Children: []config.Split{
					{Type: "v", Command: "npm test"},
				}},
			},
		}},
	}

	r := &fakeRunner{}
	name, sessionID, created, err := Create(r, cfg, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if name != "proj" {
		t.Errorf("name = %q, want proj", name)
	}
	if sessionID != "$1" {
		t.Errorf("sessionID = %q, want $1", sessionID)
	}
	if !created {
		t.Error("created = false, want true")
	}

	// Note the ordering: both siblings at the top level are created before
	// the nested child, so the child splits %2 at its full size rather than
	// after a later sibling has already carved it up. pre_window is typed once
	// per pane, and every command goes through "send-keys -l --" plus a
	// separate Enter so a command that looks like a key name is still typed.
	want := []string{
		"list-sessions -F #{session_id}|#{session_name}",
		"new-session -d -P -F #{session_id}|#{session_name}|#{window_id}|#{pane_id} -s proj -n editor -c /tmp/proj",
		// second entry splits the initial pane %1 -> %2 (breadth first)
		"split-window -d -t %1 -h -P -F #{pane_id} -l 30%",
		// first split entry: no type, reuses initial pane %1
		"send-keys -t %1 -l -- nvm use 18",
		"send-keys -t %1 Enter",
		"send-keys -t %1 -l -- nvim",
		"send-keys -t %1 Enter",
		"send-keys -t %2 -l -- nvm use 18",
		"send-keys -t %2 Enter",
		"send-keys -t %2 -l -- npm run dev",
		"send-keys -t %2 Enter",
		// child splits its parent %2 -> %3
		"split-window -d -t %2 -v -P -F #{pane_id}",
		"send-keys -t %3 -l -- nvm use 18",
		"send-keys -t %3 Enter",
		"send-keys -t %3 -l -- npm test",
		"send-keys -t %3 Enter",
		// no startup_window: land on the first window explicitly
		"select-window -t @1",
	}
	got := r.joined()
	if len(got) != len(want) {
		t.Fatalf("got %d calls, want %d:\n%s", len(got), len(want), strings.Join(got, "\n"))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("call %d:\n got %q\nwant %q", i, got[i], want[i])
		}
	}
}

func TestCreateLegacyPanes(t *testing.T) {
	cfg := &config.Config{
		Session: config.Session{Name: "proj", Root: "/tmp/proj"},
		Windows: []config.Window{{
			Name: "tests",
			Panes: []config.Pane{
				{Command: "npm test"},
				{Command: "npm run lint"},
				{Command: "# placeholder"},
			},
		}},
	}

	r := &fakeRunner{}
	if _, _, _, err := Create(r, cfg, io.Discard, io.Discard); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got := strings.Join(r.joined(), "\n")
	for _, want := range []string{
		"send-keys -t %1 -l -- npm test",
		"split-window -d -t %1 -h -P -F #{pane_id}",
		"send-keys -t %2 -l -- npm run lint",
		"split-window -d -t %2 -v -P -F #{pane_id}",
		"select-layout -t @1 tiled",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing call %q in:\n%s", want, got)
		}
	}
	// The comment command must not be typed into pane %3.
	if strings.Contains(got, "placeholder") {
		t.Errorf("comment command was sent:\n%s", got)
	}
}

func TestCreateMultipleWindowsAndStartup(t *testing.T) {
	pane := 1
	cfg := &config.Config{
		Session: config.Session{Name: "proj", Root: "/tmp/proj", StartupWindow: "second", StartupPane: &pane},
		Windows: []config.Window{
			{Name: "first"},
			{Name: "second"},
		},
	}

	// selectStartup resolves the window/pane to their tmux IDs via
	// list-windows/list-panes: window "second" is @2, and its pane at index 1
	// is %2.
	r := &fakeRunner{
		listWindowsOutput: "0|@1|0|layout|first\n1|@2|1|layout|second",
		listPanesOutput:   map[string]string{"@2": "%2|1|1|zsh"},
	}
	_, sessionID, _, err := Create(r, cfg, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got := strings.Join(r.joined(), "\n")
	for _, want := range []string{
		"new-session -d -P -F #{session_id}|#{session_name}|#{window_id}|#{pane_id} -s proj -n first -c /tmp/proj",
		"new-window -d -P -F #{window_id}|#{pane_id} -t " + sessionID + " -n second -c /tmp/proj",
		"select-window -t @2",
		"select-pane -t %2",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing call %q in:\n%s", want, got)
		}
	}
}

// TestCreateStartupWindowWithDot guards against the "." misparse: a window
// named "app.web" must be targeted by its @id, never by "session:app.web"
// (which tmux would read as window "app", pane "web").
func TestCreateStartupWindowWithDot(t *testing.T) {
	cfg := &config.Config{
		Session: config.Session{Name: "proj", Root: "/tmp/proj", StartupWindow: "app.web"},
		Windows: []config.Window{{Name: "app.web"}},
	}
	r := &fakeRunner{listWindowsOutput: "0|@1|1|layout|app.web"}
	if _, _, _, err := Create(r, cfg, io.Discard, io.Discard); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got := strings.Join(r.joined(), "\n")
	if !strings.Contains(got, "select-window -t @1") {
		t.Errorf("want select-window targeting the window ID @1, got:\n%s", got)
	}
	for _, c := range r.calls {
		for i, arg := range c {
			if i > 0 && c[i-1] == "-t" && strings.Contains(arg, "app.web") {
				t.Errorf("call %v targets the raw dotted window name instead of an ID", c)
			}
		}
	}
}

func TestCreateDerivesNameFromRoot(t *testing.T) {
	cfg := &config.Config{
		Session: config.Session{Root: "/tmp/derived-name"},
		Windows: []config.Window{{Name: "w"}},
	}
	r := &fakeRunner{}
	name, _, _, err := Create(r, cfg, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if name != "derived-name" {
		t.Errorf("name = %q, want derived-name", name)
	}
}

func TestCreateRequiresWindows(t *testing.T) {
	cfg := &config.Config{Session: config.Session{Name: "x"}}
	if _, _, _, err := Create(&fakeRunner{}, cfg, io.Discard, io.Discard); err == nil {
		t.Error("Create with no windows: want error, got nil")
	}
}

func TestCreateLeavesRunningSessionUntouched(t *testing.T) {
	cfg := &config.Config{
		Session: config.Session{Name: "proj", Root: "/tmp/proj"},
		Windows: []config.Window{{Name: "w", Splits: []config.Split{{Command: "nvim"}}}},
	}

	r := &fakeRunner{listOutput: "$9|proj"}
	name, sessionID, created, err := Create(r, cfg, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if name != "proj" {
		t.Errorf("name = %q, want proj", name)
	}
	if sessionID != "$9" {
		t.Errorf("sessionID = %q, want $9", sessionID)
	}
	if created {
		t.Error("created = true, want false for a running session")
	}
	got := r.joined()
	if len(got) != 1 || got[0] != "list-sessions -F #{session_id}|#{session_name}" {
		t.Errorf("running session must only be probed, got calls:\n%s", strings.Join(got, "\n"))
	}
}

// TestCreateLeavesRunningSessionUntouchedDottedName guards against the bug
// where a session name containing "." (e.g. "wyrm.vim") couldn't be found by
// name: tmux's -t target syntax misparses such names, so the existence check
// must match by comparing session_name strings in Go (via
// tmux.FindSessionID), not by passing the name through -t.
func TestCreateLeavesRunningSessionUntouchedDottedName(t *testing.T) {
	cfg := &config.Config{
		Session: config.Session{Name: "wyrm.vim", Root: "/tmp/proj"},
		Windows: []config.Window{{Name: "w"}},
	}

	r := &fakeRunner{listOutput: "$4|wyrm.vim"}
	name, sessionID, created, err := Create(r, cfg, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if name != "wyrm.vim" || sessionID != "$4" || created {
		t.Errorf("Create = %q, %q, %v; want wyrm.vim, $4, false", name, sessionID, created)
	}
}

func TestKill(t *testing.T) {
	cfg := &config.Config{
		Session: config.Session{Name: "proj", Root: "/tmp/proj"},
		Windows: []config.Window{{Name: "w"}},
	}

	t.Run("not running", func(t *testing.T) {
		r := &fakeRunner{}
		if _, err := Kill(r, cfg, io.Discard); err == nil {
			t.Fatal("want error for missing session")
		}
		for _, c := range r.joined() {
			if strings.HasPrefix(c, "kill-session") {
				t.Errorf("kill-session called for missing session: %q", c)
			}
		}
	})

	t.Run("running", func(t *testing.T) {
		r := &fakeRunner{listOutput: "$7|proj"}
		name, err := Kill(r, cfg, io.Discard)
		if err != nil {
			t.Fatalf("Kill: %v", err)
		}
		if name != "proj" {
			t.Errorf("name = %q, want proj", name)
		}
		got := strings.Join(r.joined(), "\n")
		if !strings.Contains(got, "kill-session -t $7") {
			t.Errorf("missing kill-session call:\n%s", got)
		}
	})

	t.Run("on_project_exit failure still kills session", func(t *testing.T) {
		exitCfg := &config.Config{
			Session: config.Session{Name: "proj", Root: "/tmp/proj", OnProjectExit: "exit 1"},
			Windows: []config.Window{{Name: "w"}},
		}
		r := &fakeRunner{listOutput: "$7|proj"}
		var stderr bytes.Buffer
		name, err := Kill(r, exitCfg, &stderr)
		if err != nil {
			t.Fatalf("Kill: %v", err)
		}
		if name != "proj" {
			t.Errorf("name = %q, want proj", name)
		}
		if !strings.Contains(stderr.String(), "on_project_exit failed") {
			t.Errorf("stderr = %q, want on_project_exit failure warning", stderr.String())
		}
	})

	t.Run("kill-session error", func(t *testing.T) {
		r := &fakeRunner{listOutput: "$7|proj", fail: map[string]bool{"kill-session": true}}
		if _, err := Kill(r, cfg, io.Discard); err == nil || !strings.Contains(err.Error(), "killing session") {
			t.Errorf("Kill error = %v, want containing %q", err, "killing session")
		}
	})
}

func TestCreateOnProjectStartFailureStillCreates(t *testing.T) {
	cfg := &config.Config{
		Session: config.Session{Name: "proj", Root: "/tmp/proj", OnProjectStart: "exit 1"},
		Windows: []config.Window{{Name: "w"}},
	}
	r := &fakeRunner{}
	var stderr bytes.Buffer
	name, _, created, err := Create(r, cfg, io.Discard, &stderr)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if name != "proj" || !created {
		t.Errorf("name, created = %q, %v; want proj, true", name, created)
	}
	if !strings.Contains(stderr.String(), "on_project_start failed") {
		t.Errorf("stderr = %q, want on_project_start failure warning", stderr.String())
	}
}

func TestCreateNewSessionError(t *testing.T) {
	cfg := &config.Config{
		Session: config.Session{Name: "proj", Root: "/tmp/proj"},
		Windows: []config.Window{{Name: "w"}},
	}
	r := &fakeRunner{fail: map[string]bool{"new-session": true}}
	if _, _, _, err := Create(r, cfg, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "creating session") {
		t.Errorf("Create error = %v, want containing %q", err, "creating session")
	}
}

func TestCreateNewWindowError(t *testing.T) {
	cfg := &config.Config{
		Session: config.Session{Name: "proj", Root: "/tmp/proj"},
		Windows: []config.Window{{Name: "first"}, {Name: "second"}},
	}
	r := &fakeRunner{fail: map[string]bool{"new-window": true}}
	_, _, _, err := Create(r, cfg, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), `creating window "second"`) {
		t.Errorf("Create error = %v, want containing %q", err, `creating window "second"`)
	}
}

func TestCreateUnexpectedOutput(t *testing.T) {
	cfg := &config.Config{
		Session: config.Session{Name: "proj", Root: "/tmp/proj"},
		Windows: []config.Window{{Name: "w"}},
	}
	r := &fakeRunner{badNewSessionOutput: true}
	if _, _, _, err := Create(r, cfg, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "unexpected tmux output") {
		t.Errorf("Create error = %v, want containing %q", err, "unexpected tmux output")
	}
}

func TestCreatePreWindowOnly(t *testing.T) {
	cfg := &config.Config{
		Session: config.Session{Name: "proj", Root: "/tmp/proj"},
		Windows: []config.Window{{Name: "w", PreWindow: "echo hi"}},
	}
	r := &fakeRunner{}
	if _, _, _, err := Create(r, cfg, io.Discard, io.Discard); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got := r.joined()
	want := "send-keys -t %1 -l -- echo hi"
	count := 0
	for _, c := range got {
		if c == want {
			count++
		}
	}
	if count != 1 {
		t.Errorf("send-keys %q called %d times in:\n%s", want, count, strings.Join(got, "\n"))
	}
}

func TestApplySplitsSplitError(t *testing.T) {
	cfg := &config.Config{
		Session: config.Session{Name: "proj", Root: "/tmp/proj"},
		Windows: []config.Window{{
			Name: "w",
			Splits: []config.Split{
				{Type: "h", Command: "should-not-run"},
				{Command: "second-entry"},
			},
		}},
	}
	r := &fakeRunner{fail: map[string]bool{"split-window": true}}
	if _, _, _, err := Create(r, cfg, io.Discard, io.Discard); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got := strings.Join(r.joined(), "\n")
	if strings.Contains(got, "should-not-run") {
		t.Errorf("command sent to a pane that failed to split:\n%s", got)
	}
	// The second, typeless entry reuses the base pane and must still run.
	if !strings.Contains(got, "second-entry") {
		t.Errorf("missing command for sibling entry after split failure:\n%s", got)
	}
}

func TestApplyPanesSplitError(t *testing.T) {
	cfg := &config.Config{
		Session: config.Session{Name: "proj", Root: "/tmp/proj"},
		Windows: []config.Window{{
			Name: "w",
			Panes: []config.Pane{
				{Command: "first"},
				{Command: "should-not-run"},
			},
		}},
	}
	r := &fakeRunner{fail: map[string]bool{"split-window": true}}
	if _, _, _, err := Create(r, cfg, io.Discard, io.Discard); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got := strings.Join(r.joined(), "\n")
	if !strings.Contains(got, "send-keys -t %1 -l -- first") {
		t.Errorf("missing first pane command:\n%s", got)
	}
	if strings.Contains(got, "should-not-run") {
		t.Errorf("command sent to a pane that failed to split:\n%s", got)
	}
}

func TestApplyPanesSelectLayoutError(t *testing.T) {
	cfg := &config.Config{
		Session: config.Session{Name: "proj", Root: "/tmp/proj"},
		Windows: []config.Window{{
			Name:  "w",
			Panes: []config.Pane{{Command: "a"}, {Command: "b"}},
		}},
	}
	r := &fakeRunner{fail: map[string]bool{"select-layout": true}}
	if _, _, _, err := Create(r, cfg, io.Discard, io.Discard); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

func TestSendKeysError(t *testing.T) {
	cfg := &config.Config{
		Session: config.Session{Name: "proj", Root: "/tmp/proj"},
		Windows: []config.Window{{Name: "w", Splits: []config.Split{{Command: "nvim"}}}},
	}
	r := &fakeRunner{fail: map[string]bool{"send-keys": true}}
	if _, _, _, err := Create(r, cfg, io.Discard, io.Discard); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

func TestSelectStartupWindowNotFound(t *testing.T) {
	cfg := &config.Config{
		Session: config.Session{Name: "proj", Root: "/tmp/proj", StartupWindow: "missing"},
		Windows: []config.Window{{Name: "w"}},
	}
	// The live window list has "w" but not "missing", so no select-window fires.
	r := &fakeRunner{listWindowsOutput: "0|@1|1|layout|w"}
	if _, _, _, err := Create(r, cfg, io.Discard, io.Discard); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got := strings.Join(r.joined(), "\n")
	if strings.Contains(got, "select-window") {
		t.Errorf("select-window called for a startup_window that isn't in the window list:\n%s", got)
	}
}

func TestSelectStartupSelectWindowError(t *testing.T) {
	cfg := &config.Config{
		Session: config.Session{Name: "proj", Root: "/tmp/proj", StartupWindow: "w"},
		Windows: []config.Window{{Name: "w"}},
	}
	r := &fakeRunner{
		listWindowsOutput: "0|@1|1|layout|w",
		fail:              map[string]bool{"select-window": true},
	}
	if _, _, _, err := Create(r, cfg, io.Discard, io.Discard); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got := strings.Join(r.joined(), "\n")
	if strings.Contains(got, "select-pane") {
		t.Errorf("select-pane called after select-window failure:\n%s", got)
	}
}

func TestSelectStartupSelectPaneError(t *testing.T) {
	pane := 0
	cfg := &config.Config{
		Session: config.Session{Name: "proj", Root: "/tmp/proj", StartupWindow: "w", StartupPane: &pane},
		Windows: []config.Window{{Name: "w"}},
	}
	r := &fakeRunner{
		listWindowsOutput: "0|@1|1|layout|w",
		listPanesOutput:   map[string]string{"@1": "%1|0|1|zsh"},
		fail:              map[string]bool{"select-pane": true},
	}
	if _, _, _, err := Create(r, cfg, io.Discard, io.Discard); err != nil {
		t.Fatalf("Create: %v", err)
	}
}
