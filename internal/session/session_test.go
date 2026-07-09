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
}

func (f *fakeRunner) Run(args ...string) (string, error) {
	f.calls = append(f.calls, args)
	switch args[0] {
	case "new-session", "new-window":
		f.winSeq++
		f.paneSeq++
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
	name, reattached, err := Create(r, cfg)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if name != "proj" {
		t.Errorf("name = %q, want proj", name)
	}
	if reattached {
		t.Error("reattached = true, want false for a fresh session")
	}

	want := []string{
		"has-session -t proj",
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

func TestCreateReattachesExistingSession(t *testing.T) {
	cfg := &config.Config{
		Session: config.Session{Name: "proj", Root: "/tmp/proj"},
		Windows: []config.Window{{Name: "editor", Splits: []config.Split{{Command: "nvim"}}}},
	}

	r := &fakeRunner{hasSession: true}
	name, reattached, err := Create(r, cfg)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if name != "proj" {
		t.Errorf("name = %q, want proj", name)
	}
	if !reattached {
		t.Error("reattached = false, want true for an already-running session")
	}

	want := []string{"has-session -t proj"}
	got := r.joined()
	if len(got) != len(want) {
		t.Fatalf("got %d calls, want %d (no rebuild):\n%s", len(got), len(want), strings.Join(got, "\n"))
	}
	if got[0] != want[0] {
		t.Errorf("call 0 = %q, want %q", got[0], want[0])
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
}
