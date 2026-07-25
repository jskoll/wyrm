package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jskoll/wyrm/internal/config"
	"github.com/jskoll/wyrm/internal/tmux"
)

// writeTheme puts contents at a temp path and returns it.
func writeTheme(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "theme.toml")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadThemeMissingFileKeepsDefaults(t *testing.T) {
	got, err := loadThemeFile(filepath.Join(t.TempDir(), "absent.toml"))
	if err != nil {
		t.Fatalf("a missing theme file is not an error: %v", err)
	}
	if got != DefaultTheme() {
		t.Errorf("theme = %+v, want the built-in default", got)
	}
}

// A theme file names the roles it cares about; every other role has to keep
// its default rather than falling back to "no color", which lipgloss renders
// as invisible.
func TestLoadThemePartialOverride(t *testing.T) {
	path := writeTheme(t, "accent = \"#123456\"\nindex = \"#abcdef\"\n")

	got, err := loadThemeFile(path)
	if err != nil {
		t.Fatalf("loadThemeFile: %v", err)
	}
	if got.Accent != "#123456" {
		t.Errorf("accent = %q, want the override", got.Accent)
	}
	if got.Index != "#abcdef" {
		t.Errorf("index = %q, want the override", got.Index)
	}
	def := DefaultTheme()
	if got.Subtle != def.Subtle || got.Error != def.Error || got.Selected != def.Selected {
		t.Errorf("unset roles lost their defaults: %+v", got)
	}
}

func TestLoadThemeAcceptsShortHex(t *testing.T) {
	got, err := loadThemeFile(writeTheme(t, "accent = \"#abc\"\n"))
	if err != nil {
		t.Fatalf("loadThemeFile: %v", err)
	}
	if got.Accent != "#abc" {
		t.Errorf("accent = %q, want #abc", got.Accent)
	}
}

// A typo'd role would otherwise be dropped in silence, leaving the user
// staring at a color that didn't change.
func TestLoadThemeRejectsUnknownRole(t *testing.T) {
	_, err := loadThemeFile(writeTheme(t, "acccent = \"#88c0d0\"\n"))
	if err == nil {
		t.Fatal("an unknown role should be an error")
	}
	if !strings.Contains(err.Error(), "acccent") {
		t.Errorf("error should name the offending key, got: %v", err)
	}
}

func TestLoadThemeRejectsBadColor(t *testing.T) {
	for _, bad := range []string{"blue", "#12345", "#gggggg", "88c0d0", ""} {
		_, err := loadThemeFile(writeTheme(t, "accent = \""+bad+"\"\n"))
		if bad == "" {
			// An empty value means "leave this role alone", not an error.
			if err != nil {
				t.Errorf("empty accent should keep the default, got: %v", err)
			}
			continue
		}
		if err == nil {
			t.Errorf("accent = %q should be rejected", bad)
			continue
		}
		if !strings.Contains(err.Error(), "accent") {
			t.Errorf("error for %q should name the role, got: %v", bad, err)
		}
	}
}

func TestLoadThemeReportsParseErrors(t *testing.T) {
	path := writeTheme(t, "accent = \n")
	_, err := loadThemeFile(path)
	if err == nil {
		t.Fatal("malformed TOML should be an error")
	}
	// The path matters: the file isn't one the user just edited by hand
	// necessarily, and "parsing" alone wouldn't say which file.
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error should name the file, got: %v", err)
	}
}

// LoadTheme resolves the path itself; XDG_CONFIG_HOME has to be honored the
// same way the settings file honors it, or the theme lands somewhere the user
// isn't looking.
func TestLoadThemeHonorsXDGConfigHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "wyrm"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wyrm", config.ThemeFileName),
		[]byte("accent = \"#123456\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := LoadTheme()
	if err != nil {
		t.Fatalf("LoadTheme: %v", err)
	}
	if got.Accent != "#123456" {
		t.Errorf("accent = %q, want the file's override", got.Accent)
	}
}

// SetTheme has to reach the styles the panels actually render with — a loader
// that parsed the file correctly and changed nothing on screen would look
// exactly like no theme support at all.
func TestSetThemeChangesRenderedColors(t *testing.T) {
	withColor(t)
	t.Cleanup(func() { SetTheme(DefaultTheme()) })

	custom := DefaultTheme()
	custom.Index = "#654321"
	SetTheme(custom)

	m := New(nopRunner(), nil)
	m.width, m.height, m.ready = 100, 40, true
	m.focus = panelWindows
	m.windows = []tmux.WindowInfo{{Index: 0, ID: "@1", Name: "editor"}}
	m.windowCur = 0

	out := m.renderWindows(30, 8)
	if want := fgSGR(t, custom.Index); !strings.Contains(out, want) {
		t.Errorf("window index not rendered in the themed color %s:\n%q", custom.Index, out)
	}
	if old := fgSGR(t, DefaultTheme().Index); strings.Contains(out, old) {
		t.Errorf("window index still rendered in the default color:\n%q", out)
	}
}
