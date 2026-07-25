package picker

import (
	"bufio"
	"bytes"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func decode(t *testing.T, in string) (keyCode, rune) {
	t.Helper()
	k, ch, err := readKey(bufio.NewReader(strings.NewReader(in)))
	if err != nil {
		t.Fatalf("readKey(%q): %v", in, err)
	}
	return k, ch
}

// TestNavigationKeysDoNotLeakIntoQuery is the regression test for readKey
// giving up after one byte of an escape sequence: the rest was left in the
// buffer and re-read as literal text, so pressing Home/End/PgUp/PgDn silently
// typed a "~" into the fuzzy filter and a shifted arrow typed ";2A".
func TestNavigationKeysDoNotLeakIntoQuery(t *testing.T) {
	sequences := map[string]string{
		"Home":        "\x1b[1~",
		"End":         "\x1b[4~",
		"PgUp":        "\x1b[5~",
		"PgDn":        "\x1b[6~",
		"Delete":      "\x1b[3~",
		"Shift-Up":    "\x1b[1;2A",
		"Home (CSI)":  "\x1b[H",
		"End (CSI)":   "\x1b[F",
		"mouse press": "\x1b[<0;10;5M",
	}
	for name, seq := range sequences {
		t.Run(name, func(t *testing.T) {
			br := bufio.NewReader(strings.NewReader(seq))
			k, _, err := readKey(br)
			if err != nil {
				t.Fatalf("readKey: %v", err)
			}
			if k == keyRune {
				t.Errorf("%s decoded as a typed rune", name)
			}
			if n := br.Buffered(); n != 0 {
				rest := make([]byte, n)
				_, _ = br.Read(rest)
				t.Errorf("%s left %q unconsumed — it would be typed into the query", name, rest)
			}
		})
	}
}

// TestDeleteNoLongerKills: Delete used to map straight to "kill session",
// unconfirmed and undocumented, one key away from Home and End.
func TestDeleteNoLongerKills(t *testing.T) {
	if k, _ := decode(t, "\x1b[3~"); k == keyKill {
		t.Error("Delete still maps to kill-session")
	}
}

func TestArrowKeysStillDecode(t *testing.T) {
	if k, _ := decode(t, "\x1b[A"); k != keyUp {
		t.Errorf("up arrow = %v, want keyUp", k)
	}
	if k, _ := decode(t, "\x1b[B"); k != keyDown {
		t.Errorf("down arrow = %v, want keyDown", k)
	}
	if k, _ := decode(t, "\x1bOA"); k != keyUp {
		t.Errorf("application-mode up = %v, want keyUp", k)
	}
}

func TestCtrlUClearsQuery(t *testing.T) {
	if k, _ := decode(t, "\x15"); k != keyClearQuery {
		t.Errorf("Ctrl-U = %v, want keyClearQuery", k)
	}
}

// TestKillRequiresConfirmation: Ctrl-X asks first now, matching the TUI. The
// picker used to destroy the highlighted session outright.
func TestKillRequiresConfirmation(t *testing.T) {
	sessions := []Session{{ID: "$1", Name: "alpha"}, {ID: "$2", Name: "beta"}}
	r := &scriptedRunner{out: map[string]string{"list-sessions": "$2|1|0|1000|beta"}}

	// Ctrl-X then "n" declines, then Esc quits.
	br := bufio.NewReader(strings.NewReader("\x18n\x1b"))
	if _, err := runLoop(r, sessions, br, &renderer{w: &bytes.Buffer{}}, fixedHeight, nil); err != nil {
		t.Fatalf("runLoop: %v", err)
	}
	if r.called("kill-session", "$1") {
		t.Errorf("session killed despite declining the confirmation: %v", r.calls)
	}
}

func TestKillConfirmationPromptIsShown(t *testing.T) {
	sessions := []Session{{ID: "$1", Name: "alpha"}}
	var out bytes.Buffer
	// Ctrl-X draws the prompt, "n" declines it, Esc quits.
	br := bufio.NewReader(strings.NewReader("\x18n\x1b"))
	if _, err := runLoop(&scriptedRunner{}, sessions, br, &renderer{w: &out}, fixedHeight, nil); err != nil {
		t.Fatalf("runLoop: %v", err)
	}
	if !strings.Contains(out.String(), "kill session 'alpha'?") {
		t.Errorf("no confirmation prompt drawn:\n%s", out.String())
	}
}

// TestFormatRowAlignsByDisplayWidth: fmt's "%-24s" pads by rune count, so a
// CJK or emoji name — twice as wide on screen as it is long in runes — pushed
// the window count out of alignment on every row.
func TestFormatRowAlignsByDisplayWidth(t *testing.T) {
	wide := FormatRow(Session{Name: "日本語セッション", Windows: 2}, false)
	ascii := FormatRow(Session{Name: "plain-session", Windows: 2}, false)

	wideCol := countColumn(t, wide)
	asciiCol := countColumn(t, ascii)
	if wideCol != asciiCol {
		t.Errorf("window count starts at column %d for a wide name and %d for an ascii one", wideCol, asciiCol)
	}
}

// countColumn returns the display column the window count starts at.
func countColumn(t *testing.T, row string) int {
	t.Helper()
	i := strings.Index(row, "2 windows")
	if i < 0 {
		t.Fatalf("row has no window count: %q", row)
	}
	return ansi.StringWidth(row[:i])
}

// TestFormatRowTruncatesLongNames: a long name used to run over the window
// count and the attached marker, which autowrap-off then clipped away with no
// sign anything was missing.
func TestFormatRowTruncatesLongNames(t *testing.T) {
	row := FormatRow(Session{Name: strings.Repeat("x", 60), Windows: 3, Attached: true}, false)
	if !strings.Contains(row, "3 windows") {
		t.Errorf("window count lost to a long name: %q", row)
	}
	if !strings.Contains(row, "(attached)") {
		t.Errorf("attached marker lost to a long name: %q", row)
	}
	if !strings.Contains(row, "…") {
		t.Errorf("long name not marked as truncated: %q", row)
	}
}
