package tui

import "github.com/charmbracelet/lipgloss"

// Theme is the TUI's palette, one entry per role rather than per color: the
// same value can play several roles, and a user overriding "the color of the
// focused panel" shouldn't have to know which palette slot that came from.
//
// Every field is a hex string ("#rgb" or "#rrggbb"). Empty fields keep the
// built-in default, so a theme file can override one role and leave the rest
// alone — see LoadTheme.
type Theme struct {
	Accent   string `toml:"accent"`   // focused panel border + title, live preview title
	Subtle   string `toml:"subtle"`   // blurred borders, hints, help footer
	Filter   string `toml:"filter"`   // the panel being filtered: border, title, prompt
	Selected string `toml:"selected"` // selection band in the focused panel
	Text     string `toml:"text"`     // text on the selection band
	Trail    string `toml:"trail"`    // selection band in the other panels
	Index    string `toml:"index"`    // window indices and pane IDs
	Active   string `toml:"active"`   // running / attached dots
	Error    string `toml:"error"`    // failed actions
	Blocked  string `toml:"blocked"`  // agent stopped on a prompt it can't answer
	Idle     string `toml:"idle"`     // agent finished its turn, awaiting input
}

// Nord (https://www.nordtheme.com), MIT-licensed, is the built-in default:
// the polar-night grays for the two selection bands, frost for the focused
// accent and identifiers, and aurora for status. It's the default because
// it's freely redistributable — a paid theme's palette can't be shipped in
// this repo, but any theme can be dropped in through a theme file.
const (
	nord1  = "#3b4252" // polar night, one step up from the background
	nord2  = "#434c5e" // polar night, selection
	nord3  = "#4c566a" // polar night, de-emphasized UI
	nord6  = "#eceff4" // snow storm, brightest text
	nord7  = "#8fbcbb" // frost, teal
	nord8  = "#88c0d0" // frost, the signature accent
	nord11 = "#bf616a" // aurora, red
	nord13 = "#ebcb8b" // aurora, yellow
	nord14 = "#a3be8c" // aurora, green
)

// DefaultTheme returns the built-in Nord palette. It's a function rather than
// a var so callers can't mutate the fallback other themes are layered onto.
func DefaultTheme() Theme {
	return Theme{
		Accent:   nord8,
		Subtle:   nord3,
		Filter:   nord13,
		Selected: nord2,
		Text:     nord6,
		Trail:    nord1,
		Index:    nord7,
		Active:   nord14,
		Error:    nord11,
		// Yellow for blocked so it reads as "stop, you're needed", against the
		// green of an agent that merely finished. Idle deliberately does not
		// reuse Active's green: the running/attached dot is already that color
		// and sits on the same rows.
		Blocked: nord13,
		Idle:    nord7,
	}
}

// The active styles. They're package-level because every render function
// reaches for them and the theme is fixed for the life of the process: it's
// read once, before the Bubble Tea program starts (see Run), and never
// changes after. SetTheme is the only thing that writes them.
var (
	focusedBorder lipgloss.Style
	blurredBorder lipgloss.Style
	filterBorder  lipgloss.Style

	focusedTitle lipgloss.Style
	blurredTitle lipgloss.Style
	filterTitle  lipgloss.Style

	// selectedRow highlights the cursor row with a background wash rather than
	// reverse video, so spans that carry their own foreground (the window
	// index, the status dot) keep their color while sitting on the selection
	// band — Inherit only fills in what a span leaves unset.
	selectedRow lipgloss.Style
	// trailRow marks the selection in a panel that doesn't have focus. Without
	// it the cascade loses its point: while the cursor is in Panes you can't
	// see which session or window you drilled through to get there. A dimmer
	// band than selectedRow, plus bold, so the focused panel still stands out.
	trailRow lipgloss.Style

	// filterStyle is the footer's filter line — the same accent as the border
	// of the panel being filtered, so the two read as one state.
	filterStyle lipgloss.Style

	hintStyle  lipgloss.Style
	helpStyle  lipgloss.Style
	errorStyle lipgloss.Style
	modalStyle lipgloss.Style
	infoStyle  lipgloss.Style
	keyStyle   lipgloss.Style

	searchMatchStyle lipgloss.Style

	activeMark lipgloss.Style // "●" running/attached
	indexMark  lipgloss.Style // window/pane identifiers

	blockedMark lipgloss.Style // "⏸" agent waiting on an answer
	idleMark    lipgloss.Style // "✓" agent finished its turn

	menuBorder   lipgloss.Style // the right-click context menu's box
	menuItem     lipgloss.Style // an unselected menu entry
	menuSelected lipgloss.Style // the highlighted menu entry
	menuKey      lipgloss.Style // the keyboard equivalent shown beside an entry
)

func init() { SetTheme(DefaultTheme()) }

// SetTheme rebuilds the styles from t. Call it before the program starts;
// mid-render it would tear the frame it's called from.
func SetTheme(t Theme) {
	accent := lipgloss.Color(t.Accent)
	subtle := lipgloss.Color(t.Subtle)
	filter := lipgloss.Color(t.Filter)

	border := lipgloss.NewStyle().Border(lipgloss.RoundedBorder())
	focusedBorder = border.BorderForeground(accent)
	blurredBorder = border.BorderForeground(subtle)
	filterBorder = border.BorderForeground(filter)

	title := lipgloss.NewStyle().Bold(true)
	focusedTitle = title.Foreground(accent)
	blurredTitle = title.Foreground(subtle)
	filterTitle = title.Foreground(filter)

	selectedRow = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Text)).Background(lipgloss.Color(t.Selected))
	trailRow = lipgloss.NewStyle().Bold(true).Background(lipgloss.Color(t.Trail))

	filterStyle = title.Foreground(filter)
	hintStyle = lipgloss.NewStyle().Foreground(subtle)
	helpStyle = lipgloss.NewStyle().Foreground(subtle)
	errorStyle = title.Foreground(lipgloss.Color(t.Error))
	modalStyle = title.Foreground(accent)
	infoStyle = title.Foreground(lipgloss.Color(t.Active))
	keyStyle = lipgloss.NewStyle().Bold(true)

	searchMatchStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Text)).Background(filter).Bold(true)

	activeMark = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Active))
	indexMark = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Index))

	blockedMark = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(t.Blocked))
	idleMark = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Idle))

	menuBorder = border.BorderForeground(accent)
	menuItem = lipgloss.NewStyle()
	menuSelected = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Text)).Background(lipgloss.Color(t.Selected))
	menuKey = lipgloss.NewStyle().Foreground(subtle)
}
