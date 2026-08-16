package freeze

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/jskoll/wyrm/internal/config"
)

// fakeRunner serves canned list-windows/list-panes output keyed by the -t
// target, mirroring the fakeRunner pattern used by internal/session and
// main's own tests.
type fakeRunner struct {
	windowsBySession map[string]string // sessionID -> list-windows -F output
	panesByWindow    map[string]string // windowID -> list-panes -F output
	fail             map[string]bool
}

func (f *fakeRunner) Run(args ...string) (string, error) {
	if f.fail[args[0]] {
		return "boom", errors.New("exit status 1")
	}
	if len(args) < 3 {
		return "", nil
	}
	target := args[2]
	switch args[0] {
	case "list-windows":
		return f.windowsBySession[target], nil
	case "list-panes":
		return f.panesByWindow[target], nil
	}
	return "", nil
}

func TestConfigTwoWindows(t *testing.T) {
	r := &fakeRunner{
		windowsBySession: map[string]string{
			"$3": strings.Join([]string{
				"0|@1|0|abcd,139x50,0,0,0|editor",
				"1|@2|1|efgh,139x50,0,0{69x50,0,0,1,69x50,70,0,2}|server",
			}, "\n"),
		},
		panesByWindow: map[string]string{
			"@1": "%0|0|1|nvim|/path/to/myproj",
			"@2": "%1|0|0|npm|/path/to/myproj\n%2|1|1|htop|/path/to/myproj",
		},
	}

	cfg, err := Config(r, "$3", "myproj", "/path/to/myproj")
	if err != nil {
		t.Fatalf("Config: %v", err)
	}

	want := &config.Config{
		Session: config.Session{
			Name:          "myproj",
			Root:          "/path/to/myproj",
			StartupWindow: "server",
			StartupPane:   intPtr(1),
		},
		Windows: []config.Window{
			{Name: "editor", Splits: []config.Split{{Command: "nvim"}}},
			{Name: "server", Splits: []config.Split{
				{Command: "npm"},
				{Type: "h", Size: 50, Command: "htop"},
			}},
		},
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("Config() =\n%+v\nwant\n%+v", cfg, want)
	}
}

func TestConfigWindowAndSplitRoots(t *testing.T) {
	r := &fakeRunner{
		windowsBySession: map[string]string{
			"$3": strings.Join([]string{
				"0|@1|0|abcd,139x50,0,0,0|web",
				"1|@2|1|efgh,139x50,0,0{69x50,0,0,1,69x50,70,0,2}|mixed",
			}, "\n"),
		},
		panesByWindow: map[string]string{
			"@1": "%0|0|1|npm run dev|/path/to/myproj/frontend",
			"@2": "%1|0|0|cargo run|/path/to/myproj/backend\n%2|1|1|python run.py|/path/to/myproj/ai",
		},
	}

	cfg, err := Config(r, "$3", "myproj", "/path/to/myproj")
	if err != nil {
		t.Fatalf("Config: %v", err)
	}

	want := &config.Config{
		Session: config.Session{
			Name:          "myproj",
			Root:          "/path/to/myproj",
			StartupWindow: "mixed",
			StartupPane:   intPtr(1),
		},
		Windows: []config.Window{
			{
				Name: "web",
				Root: "frontend",
				Splits: []config.Split{
					{Command: "npm run dev"},
				},
			},
			{
				Name: "mixed",
				Splits: []config.Split{
					{Command: "cargo run", Root: "backend"},
					{Type: "h", Size: 50, Command: "python run.py", Root: "ai"},
				},
			},
		},
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("Config() =\n%+v\nwant\n%+v", cfg, want)
	}
}

func intPtr(i int) *int { return &i }

func TestConfigNoWindows(t *testing.T) {
	r := &fakeRunner{windowsBySession: map[string]string{}}
	if _, err := Config(r, "$3", "myproj", "."); err == nil {
		t.Error("Config() with no windows: want an error")
	}
}

func TestConfigListWindowsError(t *testing.T) {
	r := &fakeRunner{fail: map[string]bool{"list-windows": true}}
	if _, err := Config(r, "$3", "myproj", "."); err == nil {
		t.Error("Config(): want an error when list-windows fails")
	}
}

func TestConfigListPanesError(t *testing.T) {
	r := &fakeRunner{
		windowsBySession: map[string]string{
			"$3": "0|@1|1|abcd,139x50,0,0,0|editor",
		},
		fail: map[string]bool{"list-panes": true},
	}
	if _, err := Config(r, "$3", "myproj", "."); err == nil {
		t.Error("Config(): want an error when list-panes fails")
	}
}

func TestConfigBadLayout(t *testing.T) {
	r := &fakeRunner{
		windowsBySession: map[string]string{
			"$3": "0|@1|1|not-a-layout|editor",
		},
		panesByWindow: map[string]string{
			"@1": "%0|0|1|nvim",
		},
	}
	if _, err := Config(r, "$3", "myproj", "."); err == nil {
		t.Error("Config(): want an error for an unparsable layout")
	}
}
