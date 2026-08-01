package tui

import (
	"strings"
	"testing"
)

// TestPanelSpecsComplete is the guard the table-driven design needs.
//
// The nine per-panel switches it replaced failed loudly when a panel was added
// and a case forgotten — the compiler said nothing, but the panel visibly did
// nothing. A table fails more quietly: a missing func is a nil the callers step
// around, and a missing `child` defaults to 0, which is panelProjects, so a new
// panel would silently reset the *Projects* cursor. This asserts every entry is
// filled in.
func TestPanelSpecsComplete(t *testing.T) {
	names := map[panel]string{
		panelProjects: "projects",
		panelSessions: "sessions",
		panelWindows:  "windows",
		panelPanes:    "panes",
	}
	for p := panel(0); p < numPanels; p++ {
		name := names[p]
		if name == "" {
			t.Errorf("panel %d has no name in this test — was a panel added?", p)
			name = "?"
		}
		spec := p.spec()
		if spec.title == "" {
			t.Errorf("%s: no title", name)
		}
		if spec.length == nil {
			t.Errorf("%s: no length func", name)
		}
		if spec.rows == nil {
			t.Errorf("%s: no rows func", name)
		}
		if spec.keys == "" {
			t.Errorf("%s: no footer keys", name)
		}
		if spec.menu == nil {
			t.Errorf("%s: no menu func", name)
		}
		if spec.kill == nil {
			t.Errorf("%s: no kill func", name)
		}
		if spec.reload == nil {
			t.Errorf("%s: no reload func", name)
		}
		// child must be set deliberately. Its zero value is panelProjects, which
		// would make a forgotten entry quietly reset the wrong panel.
		if spec.child != noPanel && (spec.child <= p || spec.child >= numPanels) {
			t.Errorf("%s: child = %d, want noPanel or a later panel", name, spec.child)
		}
	}
}

// The cascade must terminate. A child cycle would hang the TUI on every
// selection change rather than fail visibly.
func TestPanelCascadeTerminates(t *testing.T) {
	for p := panel(0); p < numPanels; p++ {
		steps := 0
		for child := p.spec().child; child != noPanel; child = child.spec().child {
			steps++
			if steps > int(numPanels) {
				t.Fatalf("cascade from panel %d does not terminate", p)
			}
		}
	}
}

// Every panel's footer has to carry the shared navigation hints, or the keys
// that always work would go undocumented on some panels.
func TestPanelKeysIncludeNav(t *testing.T) {
	for p := panel(0); p < numPanels; p++ {
		if keys := p.spec().keys; keys != "" && !strings.Contains(keys, navKeys) {
			t.Errorf("panel %d footer omits the shared nav hints: %q", p, keys)
		}
	}
}
