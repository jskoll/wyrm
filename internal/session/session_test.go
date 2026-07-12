package session

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jskoll/wyrm/internal/config"
)

// fakeRunner records every tmux invocation and fabricates the outputs the
// real tmux would print for -P -F commands, handing out sequential window
// (@N) and pane (%N) IDs.
type fakeRunner struct {
	calls      [][]string
	winSeq     int
	paneSeq    int
	hasSession bool

	// fail forces the named command (args[0]) to return an error.
	fail map[string]bool
	// badNewSessionOutput makes new-session/new-window return output with
	// no "|" separator, exercising the "unexpected tmux output" path.
	badNewSessionOutput bool
}

func (f *fakeRunner) Run(args ...string) (string, error) {
	f.calls = append(f.calls, args)
	if f.fail[args[0]] {
		return "boom", errors.New("exit status 1")
	}
	switch args[0] {
	case "new-session", "new-window":
		f.winSeq++
		f.paneSeq++
		if f.badNewSessionOutput {
			return "malformed", nil
		}
		return fmt.Sprintf("@%d|%%%d", f.winSeq, f.paneSeq), nil
	case "split-window":
		f.paneSeq++
		return fmt.Sprintf("%%%d", f.paneSeq), nil
	case "has-session":
		if !f.hasSession {
			return "no such session", errors.New("exit status 1")
		}
		return "", nil
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
	name, created, err := Create(r, cfg)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if name != "proj" {
		t.Errorf("name = %q, want proj", name)
	}
	if !created {
		t.Error("created = false, want true")
	}

	want := []string{
		"has-session -t =proj",
		"new-session -d -P -F #{window_id}|#{pane_id} -s proj -n editor -c /tmp/proj",
		// first split entry: no type, reuses initial pane %1
		"send-keys -t %1 nvm use 18 Enter",
		"send-keys -t %1 nvim Enter",
		// second entry splits %1 -> %2, gets pre_window + command
		"split-window -t %1 -h -P -F #{pane_id} -l 30%",
		"send-keys -t %2 nvm use 18 Enter",
		"send-keys -t %2 npm run dev Enter",
		// child splits its parent %2 -> %3
		"split-window -t %2 -v -P -F #{pane_id}",
		"send-keys -t %3 nvm use 18 Enter",
		"send-keys -t %3 npm test Enter",
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
	if _, _, err := Create(r, cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got := strings.Join(r.joined(), "\n")
	for _, want := range []string{
		"send-keys -t %1 npm test Enter",
		"split-window -t %1 -h -P -F #{pane_id}",
		"send-keys -t %2 npm run lint Enter",
		"split-window -t %2 -v -P -F #{pane_id}",
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

	r := &fakeRunner{}
	if _, _, err := Create(r, cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got := strings.Join(r.joined(), "\n")
	for _, want := range []string{
		"new-session -d -P -F #{window_id}|#{pane_id} -s proj -n first -c /tmp/proj",
		"new-window -P -F #{window_id}|#{pane_id} -t proj -n second -c /tmp/proj",
		"select-window -t proj:second",
		"select-pane -t proj:second.1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing call %q in:\n%s", want, got)
		}
	}
}

func TestCreateDerivesNameFromRoot(t *testing.T) {
	cfg := &config.Config{
		Session: config.Session{Root: "/tmp/derived-name"},
		Windows: []config.Window{{Name: "w"}},
	}
	r := &fakeRunner{}
	name, _, err := Create(r, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if name != "derived-name" {
		t.Errorf("name = %q, want derived-name", name)
	}
}

func TestCreateRequiresWindows(t *testing.T) {
	cfg := &config.Config{Session: config.Session{Name: "x"}}
	if _, _, err := Create(&fakeRunner{}, cfg); err == nil {
		t.Error("Create with no windows: want error, got nil")
	}
}

func TestCreateLeavesRunningSessionUntouched(t *testing.T) {
	cfg := &config.Config{
		Session: config.Session{Name: "proj", Root: "/tmp/proj"},
		Windows: []config.Window{{Name: "w", Splits: []config.Split{{Command: "nvim"}}}},
	}

	r := &fakeRunner{hasSession: true}
	name, created, err := Create(r, cfg)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if name != "proj" {
		t.Errorf("name = %q, want proj", name)
	}
	if created {
		t.Error("created = true, want false for a running session")
	}
	got := r.joined()
	if len(got) != 1 || got[0] != "has-session -t =proj" {
		t.Errorf("running session must only be probed, got calls:\n%s", strings.Join(got, "\n"))
	}
}

func TestKill(t *testing.T) {
	cfg := &config.Config{
		Session: config.Session{Name: "proj", Root: "/tmp/proj"},
		Windows: []config.Window{{Name: "w"}},
	}

	t.Run("not running", func(t *testing.T) {
		r := &fakeRunner{hasSession: false}
		if _, err := Kill(r, cfg); err == nil {
			t.Fatal("want error for missing session")
		}
		for _, c := range r.joined() {
			if strings.HasPrefix(c, "kill-session") {
				t.Errorf("kill-session called for missing session: %q", c)
			}
		}
	})

	t.Run("running", func(t *testing.T) {
		r := &fakeRunner{hasSession: true}
		name, err := Kill(r, cfg)
		if err != nil {
			t.Fatalf("Kill: %v", err)
		}
		if name != "proj" {
			t.Errorf("name = %q, want proj", name)
		}
		got := strings.Join(r.joined(), "\n")
		if !strings.Contains(got, "kill-session -t proj") {
			t.Errorf("missing kill-session call:\n%s", got)
		}
	})

	t.Run("on_project_exit failure still kills session", func(t *testing.T) {
		exitCfg := &config.Config{
			Session: config.Session{Name: "proj", Root: "/tmp/proj", OnProjectExit: "exit 1"},
			Windows: []config.Window{{Name: "w"}},
		}
		r := &fakeRunner{hasSession: true}
		name, err := Kill(r, exitCfg)
		if err != nil {
			t.Fatalf("Kill: %v", err)
		}
		if name != "proj" {
			t.Errorf("name = %q, want proj", name)
		}
	})

	t.Run("kill-session error", func(t *testing.T) {
		r := &fakeRunner{hasSession: true, fail: map[string]bool{"kill-session": true}}
		if _, err := Kill(r, cfg); err == nil || !strings.Contains(err.Error(), "killing session") {
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
	name, created, err := Create(r, cfg)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if name != "proj" || !created {
		t.Errorf("name, created = %q, %v; want proj, true", name, created)
	}
}

func TestCreateNewSessionError(t *testing.T) {
	cfg := &config.Config{
		Session: config.Session{Name: "proj", Root: "/tmp/proj"},
		Windows: []config.Window{{Name: "w"}},
	}
	r := &fakeRunner{fail: map[string]bool{"new-session": true}}
	if _, _, err := Create(r, cfg); err == nil || !strings.Contains(err.Error(), "creating session") {
		t.Errorf("Create error = %v, want containing %q", err, "creating session")
	}
}

func TestCreateNewWindowError(t *testing.T) {
	cfg := &config.Config{
		Session: config.Session{Name: "proj", Root: "/tmp/proj"},
		Windows: []config.Window{{Name: "first"}, {Name: "second"}},
	}
	r := &fakeRunner{fail: map[string]bool{"new-window": true}}
	_, _, err := Create(r, cfg)
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
	if _, _, err := Create(r, cfg); err == nil || !strings.Contains(err.Error(), "unexpected tmux output") {
		t.Errorf("Create error = %v, want containing %q", err, "unexpected tmux output")
	}
}

func TestCreatePreWindowOnly(t *testing.T) {
	cfg := &config.Config{
		Session: config.Session{Name: "proj", Root: "/tmp/proj"},
		Windows: []config.Window{{Name: "w", PreWindow: "echo hi"}},
	}
	r := &fakeRunner{}
	if _, _, err := Create(r, cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got := r.joined()
	want := "send-keys -t %1 echo hi Enter"
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
	if _, _, err := Create(r, cfg); err != nil {
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
	if _, _, err := Create(r, cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got := strings.Join(r.joined(), "\n")
	if !strings.Contains(got, "send-keys -t %1 first Enter") {
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
	if _, _, err := Create(r, cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

func TestSendKeysError(t *testing.T) {
	cfg := &config.Config{
		Session: config.Session{Name: "proj", Root: "/tmp/proj"},
		Windows: []config.Window{{Name: "w", Splits: []config.Split{{Command: "nvim"}}}},
	}
	r := &fakeRunner{fail: map[string]bool{"send-keys": true}}
	if _, _, err := Create(r, cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

func TestSelectStartupInvalidWindowName(t *testing.T) {
	cfg := &config.Config{
		Session: config.Session{Name: "proj", Root: "/tmp/proj", StartupWindow: "bad;window"},
		Windows: []config.Window{{Name: "w"}},
	}
	r := &fakeRunner{}
	if _, _, err := Create(r, cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got := strings.Join(r.joined(), "\n")
	if strings.Contains(got, "select-window") {
		t.Errorf("select-window called for invalid startup_window:\n%s", got)
	}
}

func TestSelectStartupSelectWindowError(t *testing.T) {
	cfg := &config.Config{
		Session: config.Session{Name: "proj", Root: "/tmp/proj", StartupWindow: "w"},
		Windows: []config.Window{{Name: "w"}},
	}
	r := &fakeRunner{fail: map[string]bool{"select-window": true}}
	if _, _, err := Create(r, cfg); err != nil {
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
	r := &fakeRunner{fail: map[string]bool{"select-pane": true}}
	if _, _, err := Create(r, cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}
}
