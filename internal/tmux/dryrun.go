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
type DryRun struct {
	w         io.Writer
	sessions  int
	windows   int
	panes     int
	sessionNm string
}

// NewDryRun returns a DryRun writing its transcript to w.
func NewDryRun(w io.Writer) *DryRun { return &DryRun{w: w} }

// Run implements Runner.
func (d *DryRun) Run(args ...string) (string, error) {
	_, _ = fmt.Fprintln(d.w, "tmux "+strings.Join(shellQuote(args), " "))

	if len(args) > 0 && args[0] == "list-sessions" {
		// Report the same failure a real tmux gives with no server up, so
		// FindSessionID treats it as "not running" rather than an error.
		return "no server running on /dev/null", ErrNoServer
	}
	if name, ok := flagValue(args, "-s"); ok {
		d.sessionNm = name
	}
	format, ok := flagValue(args, "-F")
	if !ok {
		return "", nil
	}
	return d.fill(format), nil
}

// fill substitutes synthetic values for the format specifiers wyrm actually
// asks for. Anything else is left alone rather than guessed at.
func (d *DryRun) fill(format string) string {
	out := format
	if strings.Contains(out, "#{session_id}") {
		d.sessions++
		out = strings.ReplaceAll(out, "#{session_id}", string(SessionSigil)+strconv.Itoa(d.sessions))
	}
	if strings.Contains(out, "#{window_id}") {
		d.windows++
		out = strings.ReplaceAll(out, "#{window_id}", string(WindowSigil)+strconv.Itoa(d.windows))
	}
	if strings.Contains(out, "#{pane_id}") {
		d.panes++
		out = strings.ReplaceAll(out, "#{pane_id}", string(PaneSigil)+strconv.Itoa(d.panes))
	}
	out = strings.ReplaceAll(out, "#{session_name}", d.sessionNm)
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
