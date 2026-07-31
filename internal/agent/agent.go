// Package agent works out what an AI coding agent running inside a tmux pane is
// doing, so the TUI can flag the sessions, windows, and panes that are waiting
// on the user instead of making them tour every pane to find out.
//
// Detection is deliberately content-based. tmux hands out a pane's title
// (#{pane_title}) in the same list-panes call the TUI already makes, which would
// be far cheaper to read — and Claude Code does set it. It just doesn't say what
// it looks like it says: the leading glyph is an animation frame that cycles
// between braille frames and "✳" while the agent works, and the very same "✳"
// sits at the head of an idle prompt's title ("✳ Claude Code"). The title can
// tell you an agent is there; it cannot tell you whether it's working or
// waiting. The pane's visible text can, so that's what Detect reads.
package agent

import (
	"regexp"
	"strings"
)

// State is what an agent pane is doing.
type State int

const (
	// StateNone is "not an agent pane".
	StateNone State = iota
	// StateBusy is working: the user has nothing to do.
	StateBusy
	// StateBlocked is stopped on a prompt it can't answer itself — a permission
	// request, a plan approval, a question. The most urgent state: the agent
	// makes no progress until the user answers.
	StateBlocked
	// StateIdle is "finished its turn, waiting for the next instruction".
	StateIdle
	// StateUnknown is an agent pane whose screen matched nothing we recognise.
	//
	// It exists so that not-recognised and finished-its-turn stay separate.
	// Detect used to return StateIdle by elimination, which made every failure
	// mode — an agent whose UI changed, or one added to tui.agent.commands that
	// this package has no patterns for at all — render as a confident "done,
	// come look" marker. Wrong in the most expensive direction. Unknown carries
	// no marker, so a detector that has stopped working goes quiet rather than
	// lying.
	StateUnknown
)

// NeedsUser reports whether the state is one the user has to act on. Busy panes
// deliberately get no marker: an indicator that's lit on every agent pane all
// the time is one nobody reads. Unknown gets none either — see StateUnknown.
func (s State) NeedsUser() bool { return s == StateBlocked || s == StateIdle }

func (s State) String() string {
	switch s {
	case StateBusy:
		return "busy"
	case StateBlocked:
		return "blocked"
	case StateIdle:
		return "idle"
	case StateUnknown:
		return "unknown"
	}
	return "none"
}

// Rank orders states by how much they want the user's attention, so a window or
// session can take the state of its most-urgent pane. Blocked outranks idle:
// "answer me" matters more than "I'm done". Unknown ranks with none: neither
// draws a marker, so neither should win a rollup from a pane that would.
func (s State) Rank() int {
	switch s {
	case StateBlocked:
		return 3
	case StateIdle:
		return 2
	case StateBusy:
		return 1
	}
	return 0
}

// Merge returns whichever of the two states is more urgent. It's the
// aggregation rule for rolling pane states up to their window and session.
func Merge(a, b State) State {
	if b.Rank() > a.Rank() {
		return b
	}
	return a
}

// DefaultCommands are the #{pane_current_command} values treated as an agent
// pane. Overridable through settings, because which binary an agent shows up as
// is a property of the user's setup, not of wyrm.
var DefaultCommands = []string{"claude"}

// IsAgent reports whether a pane running command should be inspected. commands
// falls back to DefaultCommands when empty. The comparison is case-insensitive
// and ignores a leading path, since tmux reports the bare command name but a
// wrapper may not.
func IsAgent(command string, commands []string) bool {
	if command == "" {
		return false
	}
	if len(commands) == 0 {
		commands = DefaultCommands
	}
	name := strings.ToLower(command)
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	for _, c := range commands {
		if name == strings.ToLower(strings.TrimSpace(c)) {
			return true
		}
	}
	return false
}

// How many non-blank lines up from the bottom of the pane each kind of marker
// is looked for. Two windows rather than one, because the two kinds of evidence
// sit at different heights:
//
//   - A prompt is always pinned to the bottom: it replaces the input box. A
//     narrow window keeps an already-answered prompt still visible further up
//     from re-reading as live.
//   - The working spinner is not. It renders above whatever the turn is
//     currently drawing — a todo list, streamed tool output — which on a long
//     run pushes it a dozen lines clear of the bottom.
//
// Both are far short of a screenful, because capture-pane returns the visible
// region and the top of it is finished transcript, not state.
const (
	blockedTail = 12
	busyTail    = 24
)

// busyMarkers are hints the agent only renders while a turn is in flight. They
// live in the input box, which an open prompt replaces — so a blocked pane
// shows none of them.
var busyMarkers = []string{
	"esc to interrupt",
	"tab to amend",
	"ctrl+e to explain",
}

// elapsed matches the working spinner's live counter, as in
// "✻ Scurrying… (27s · ↓ 1.6k tokens)" or, once a turn has been going a while,
// "✳ Create the script… (19m 24s · ↓ 93.7k tokens)". The parenthesised counter
// is the part that means "still running": the finished form, "✻ Worked for 8s",
// carries no parentheses and stays on screen afterwards, so it must not match.
//
// The hour/minute parts are optional and each is its own group rather than one
// loose "digits and letters" pattern: an earlier version anchored on "(\d+s"
// and so stopped recognising a turn as busy the moment it passed a minute and
// the counter became "(19m 24s" — every long-running agent then reported itself
// idle, which is precisely backwards.
var elapsed = regexp.MustCompile(`\((?:\d+h\s*)?(?:\d+m\s*)?\d+s[\s·)]`)

// blockedMarkers is the agent's own prompt chrome — text it draws around a
// selector, which no transcript reproduces.
//
// Note what isn't here, and why. The obvious candidates are the question
// phrasings themselves ("do you want to", "would you like to"), and they cannot
// be used: a pane is a screenful of arbitrary text, and an agent that is merely
// *displaying* those words — reviewing a diff, writing tests about prompts,
// quoting its own earlier output — is not waiting on anything. Matching them
// flagged a busy pane as blocked in exactly that situation. The prompts they
// would have caught all carry a selector anyway, so hasOptionList catches them
// without reading prose.
//
// "esc to cancel" is absent for a different reason: the agent shows
// "Esc to cancel · Tab to amend" under the input box of a *running* turn and
// "Enter to confirm · Esc to cancel" under a selector. Only the halves unique to
// each state can be matched.
var blockedMarkers = []string{
	"enter to confirm",
}

// idleMarkers is the agent's *idle input box* — chrome it draws only when it is
// accepting typing. A running turn replaces the whole footer (see busyHints in
// the tests: "Esc to cancel · Tab to amend" stands where these normally sit),
// and an open selector replaces the box entirely.
//
// These exist so idle is reported on evidence rather than by elimination. The
// vim-mode indicators are the box's mode display; "? for shortcuts" is the
// footer it shows when vim mode is off. Between them they cover the default
// setups; anything else lands on StateUnknown and draws no marker, which is the
// honest answer.
var idleMarkers = []string{
	"-- insert --",
	"-- normal --",
	"? for shortcuts",
}

// optionOne/optionTwo match the first two rows of a numbered choice list, e.g.
//
//	❯ 1. Yes, I trust this folder
//	  2. No, exit
//
// Both are required, close together: a lone "1." line is something an agent
// prints in ordinary prose all the time, whereas a "1." immediately followed by
// a "2." at the bottom of the screen is a selector. The optional leading "│" is
// the prompt box's border, and "❯" the cursor on the highlighted option.
var (
	optionOne = regexp.MustCompile(`^\s*[│|]?\s*[❯>]?\s*1\.\s+\S`)
	optionTwo = regexp.MustCompile(`^\s*[│|]?\s*[❯>]?\s*2\.\s+\S`)
)

// optionGap is how many lines after a "1." row a matching "2." row may appear
// and still count as the same selector. Options usually sit on consecutive
// lines, but a wrapped label pushes the next one down.
const optionGap = 4

// Detect classifies a pane from the command it's running and its visible
// contents (as captured by tmux capture-pane, without escape sequences).
// commands may be nil, in which case DefaultCommands applies.
//
// Every branch requires positive evidence. A pane that is running an agent but
// shows none resolves to StateUnknown, not StateIdle — see StateUnknown for why
// the difference matters.
func Detect(command, content string, commands []string) State {
	if !IsAgent(command, commands) {
		return StateNone
	}
	prompt := tail(content, blockedTail)
	work := tail(content, busyTail)

	// Order matters. The unambiguous prompt text is checked before the busy
	// hints so a prompt that opens with stale hints still on screen reads as
	// blocked, and the weaker option-list shape is checked after them so a
	// numbered list the agent merely *printed* while working can't outvote a
	// live spinner.
	if containsAny(prompt, blockedMarkers) {
		return StateBlocked
	}
	if containsAny(work, busyMarkers) || matchesAny(work, elapsed) {
		return StateBusy
	}
	if hasOptionList(prompt) {
		return StateBlocked
	}
	if containsAny(prompt, idleMarkers) {
		return StateIdle
	}
	return StateUnknown
}

// tail returns the last n non-blank lines of content, in order. Blank lines are
// dropped first so the agent's generous vertical padding doesn't push the live
// UI out of the window.
func tail(content string, n int) []string {
	all := strings.Split(content, "\n")
	out := make([]string, 0, len(all))
	for _, l := range all {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}

func containsAny(lines []string, markers []string) bool {
	for _, l := range lines {
		low := strings.ToLower(l)
		for _, mk := range markers {
			if strings.Contains(low, mk) {
				return true
			}
		}
	}
	return false
}

func matchesAny(lines []string, re *regexp.Regexp) bool {
	for _, l := range lines {
		if re.MatchString(l) {
			return true
		}
	}
	return false
}

// hasOptionList reports whether the lines contain a numbered selector — a "1."
// row with a "2." row within optionGap lines of it.
func hasOptionList(lines []string) bool {
	for i, l := range lines {
		if !optionOne.MatchString(l) {
			continue
		}
		end := i + 1 + optionGap
		if end > len(lines) {
			end = len(lines)
		}
		for _, next := range lines[i+1 : end] {
			if optionTwo.MatchString(next) {
				return true
			}
		}
	}
	return false
}
