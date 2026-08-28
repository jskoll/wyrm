package tmux

import (
	"fmt"
	"io"
	"strconv"
	"strings"
)

// DryRun is a Runner that prints the tmux commands it would run instead of
// running them. It never contacts a tmux server.
//
// Commands that ask for object IDs back (a "-P -F" format string) are answered
// with synthetic ones, so the caller's build logic walks exactly the path it
// would for real rather than bailing out on an unparseable response. Listing
// commands answer as if no server were running, which is what makes a dry run
// describe a build from scratch.
//
// It also remembers the windows and panes it has handed out, so a later
// list-windows/list-panes describes the layout the transcript just built. That
// is not cosmetic: session.selectStartup resolves startup_window and
// startup_pane through those lists, so without it `wyrm up -n` on a config with
// a startup_window printed "failed to list windows: unexpected window index"
// and silently omitted the select-window/select-pane commands it would have
// run — from the transcript that exists to say what a config does.
type DryRun struct {
	w         io.Writer
	sessions  int
	windows   int
	panes     int
	sessionNm string
	// sessionRoot is the -c of the new-session call, which is what a real
	// tmux reports back as #{session_path}.
	sessionRoot string

	// order is the windows created so far, in creation order; byID and
	// paneOwner index into it so a split-window can find the window owning
	// its target pane.
	order     []*dryWindow
	byID      map[string]*dryWindow
	paneOwner map[string]*dryWindow
}

// dryWindow is one window the transcript has created, and the panes in it.
type dryWindow struct {
	id, name string
	index    int
	panes    []string
}

// NewDryRun returns a DryRun writing its transcript to w.
func NewDryRun(w io.Writer) *DryRun {
	return &DryRun{w: w, byID: map[string]*dryWindow{}, paneOwner: map[string]*dryWindow{}}
}

// Run implements Runner.
func (d *DryRun) Run(args ...string) (string, error) {
	_, _ = fmt.Fprintln(d.w, "tmux "+strings.Join(shellQuote(args), " "))
	if len(args) == 0 {
		return "", nil
	}

	switch args[0] {
	case "list-sessions":
		// Report the same failure a real tmux gives with no server up, so
		// FindSessionID treats it as "not running" rather than an error.
		return "no server running on /dev/null", ErrNoServer
	case "list-windows":
		return d.listWindows(), nil
	case "list-panes":
		return d.listPanes(args), nil
	case "display-message":
		return d.display(args), nil
	}

	if name, ok := flagValue(args, "-s"); ok {
		d.sessionNm = name
	}
	switch args[0] {
	case "new-session":
		d.sessionRoot, _ = flagValue(args, "-c")
		d.newWindow(args)
	case "new-window":
		d.newWindow(args)
	case "split-window":
		d.splitWindow(args)
	}

	format, ok := flagValue(args, "-F")
	if !ok {
		return "", nil
	}
	return d.fill(format), nil
}

// newWindow records a window and its initial pane.
func (d *DryRun) newWindow(args []string) {
	name, _ := flagValue(args, "-n")
	d.windows++
	d.panes++
	w := &dryWindow{
		id:    string(WindowSigil) + strconv.Itoa(d.windows),
		name:  name,
		index: len(d.order),
		panes: []string{string(PaneSigil) + strconv.Itoa(d.panes)},
	}
	d.order = append(d.order, w)
	d.byID[w.id] = w
	d.paneOwner[w.panes[0]] = w
}

// splitWindow records a new pane in whichever window owns the split's target.
func (d *DryRun) splitWindow(args []string) {
	target, _ := flagValue(args, "-t")
	w := d.paneOwner[target]
	if w == nil {
		w = d.byID[target]
	}
	if w == nil && len(d.order) > 0 {
		w = d.order[len(d.order)-1]
	}
	d.panes++
	pane := string(PaneSigil) + strconv.Itoa(d.panes)
	if w != nil {
		w.panes = append(w.panes, pane)
		d.paneOwner[pane] = w
	}
}

// listWindows answers windowListFormat for every window created so far. The
// first window is reported active: every window is created with -d, so that is
// where a real tmux would still be by the time selectStartup asks.
func (d *DryRun) listWindows() string {
	var b strings.Builder
	for i, w := range d.order {
		active := "0"
		if i == 0 {
			active = "1"
		}
		fmt.Fprintf(&b, "%d|%s|%s|%s|%s\n", w.index, w.id, active, dryLayout, w.name)
	}
	return strings.TrimRight(b.String(), "\n")
}

// dryLayout stands in for #{window_layout}. Nothing in a dry run parses it —
// only freeze does, and freeze needs a real session — but the field has to be
// present and non-empty for the list to have the right shape.
const dryLayout = "0000,0x0,0,0,0"

// listPanes answers paneListFormat for the panes of the target window, or for
// every pane when the target is a session.
func (d *DryRun) listPanes(args []string) string {
	target, _ := flagValue(args, "-t")
	windows := d.order
	if w, ok := d.byID[target]; ok {
		windows = []*dryWindow{w}
	}
	var b strings.Builder
	for _, w := range windows {
		for i, p := range w.panes {
			active := "0"
			if i == 0 {
				active = "1"
			}
			fmt.Fprintf(&b, "%s|%d|%s|%s|%s\n", p, i, active, "sh", d.sessionRoot)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// display answers display-message -p, which wyrm uses for #{session_path} and
// for the current session.
func (d *DryRun) display(args []string) string {
	format, ok := flagValue(args, "-F")
	if !ok {
		return ""
	}
	return d.fill(format)
}

// fill substitutes synthetic values for the format specifiers wyrm asks for.
// Anything else is left alone rather than guessed at.
//
// The session/window/pane ID branches allocate a fresh ID, so this is only
// correct for the "-P -F" reply to a command that just created one — every
// listing path above answers from recorded state instead.
func (d *DryRun) fill(format string) string {
	out := format
	if strings.Contains(out, "#{session_id}") {
		d.sessions++
		out = strings.ReplaceAll(out, "#{session_id}", string(SessionSigil)+strconv.Itoa(d.sessions))
	}
	if strings.Contains(out, "#{window_id}") && len(d.order) > 0 {
		out = strings.ReplaceAll(out, "#{window_id}", d.order[len(d.order)-1].id)
	}
	if strings.Contains(out, "#{pane_id}") {
		out = strings.ReplaceAll(out, "#{pane_id}", string(PaneSigil)+strconv.Itoa(d.panes))
	}
	out = strings.ReplaceAll(out, "#{session_name}", d.sessionNm)
	out = strings.ReplaceAll(out, "#{session_path}", d.sessionRoot)
	return out
}

// flagValue returns the argument following flag, if present.
func flagValue(args []string, flag string) (string, bool) {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

// shellQuote makes the transcript copy-pasteable: an argument containing
// whitespace, quotes, or shell metacharacters is single-quoted so running the
// printed line by hand does the same thing wyrm would have done.
func shellQuote(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		if a != "" && !strings.ContainsAny(a, " \t\n'\"\\$`&;|<>()*?[]#~=") {
			out[i] = a
			continue
		}
		out[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return out
}
