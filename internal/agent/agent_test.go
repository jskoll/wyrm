package agent

import "testing"

// The fixtures below are trimmed from real `tmux capture-pane -p` output of
// Claude Code panes, one per state, so the patterns are tested against the text
// they were derived from rather than against a paraphrase of it.

// idleFresh is a just-started agent sitting at an empty prompt.
const idleFresh = `
 ▐▛███▜▌   Claude Code v2.1.220
▝▜█████▛▘  Opus 5 · Claude Pro
  ▘▘ ▝▝    ~/Code/wyrm

────────────────────────────────────────────────────────
❯ Try "refactor <filepath>"
────────────────────────────────────────────────────────
     Opus 5 | 8:12 AM
  -- INSERT -- ⏸ manual mode on · ← for agents
`

// idleDone is an agent that finished a turn. The spinner's past-tense line
// ("Worked for 8s") is still on screen and must not read as busy.
const idleDone = `
❯ Run the shell command: touch /tmp/marker
⏺ I'll run that.
  Ran 1 shell command
⏺ Done — /tmp/marker created (empty, 0 bytes).
✻ Worked for 8s
────────────────────────────────────────────────────────
❯
────────────────────────────────────────────────────────
     Opus 5 | [----------] 2% | 8:14 AM
  -- INSERT -- ⏸ manual mode on
`

// busySpinner is a turn in flight: the spinner carries a live elapsed counter.
const busySpinner = `
❯ For the tmux config in this repo I want to add clickable bindings
⏺ Searching for 1 pattern, reading 1 file, running 1 shell command…
  ⎿  $ tmux -V; man tmux | grep -n "range=" | head -60
✻ Scurrying… (27s · ↓ 1.6k tokens · thinking with high effort)
                                             ● high · /effort
────────────────────────────────────────────────────────
❯
────────────────────────────────────────────────────────
     Sonnet 5 | [----------] 4% | main (+0/-49) | 8:10 AM
  -- INSERT -- ⏸ manual mode on · ← for agents
`

// busyHints is the other face of a running turn: the input box swaps its normal
// footer for cancel/amend hints. Note "Esc to cancel", which the blocked
// fixtures also contain — see blockedMarkers.
const busyHints = `
❯ Add tmux clickable status bar bindings
⏺ Reading the tmux manual…
────────────────────────────────────────────────────────
❯
────────────────────────────────────────────────────────
 Esc to cancel · Tab to amend · ctrl+e to explain
`

// busyLongRun is a real capture of a turn that had been running for nineteen
// minutes. Two things about it broke earlier versions of the detector: the
// elapsed counter reads "19m 24s" rather than plain seconds, and the todo list
// the turn is drawing pushes the spinner line a dozen rows off the bottom.
const busyLongRun = `
⏺ Working through the list.
✳ Create .github/scripts/check-pinned-versions.sh… (19m 24s · ↓ 93.7k tokens)
  ⎿  ◼ Create .github/scripts/check-pinned-versions.sh
     ◻ Wire justfile + CLAUDE.md docs
     ◻ Add bats tests for sha256_of/fetch_verified
     ◻ Run verification suite
     ◻ Commit, push, and deploy
      … +6 completed
────────────────────────────────────────────────────────
❯
────────────────────────────────────────────────────────
     Sonnet 5 | [##--------] 23% | main (+133/-83) | 8:47 AM
  -- INSERT -- ⏵⏵ auto mode on (shift+tab to cycle) · ← for agents
`

// blockedTrust is the folder-trust prompt shown at startup.
const blockedTrust = `
 Do you trust the files in this folder?

 /private/tmp

 Claude Code'll be able to read, edit, and execute files here.

 ❯ 1. Yes, I trust this folder
   2. No, exit

 Enter to confirm · Esc to cancel
`

// blockedPermission is a tool-permission request mid-turn.
const blockedPermission = `
⏺ I'll update the config.
╭──────────────────────────────────────────────────────╮
│ Edit file                                            │
│ ~/Code/wyrm/.wyrm.toml                               │
│                                                      │
│ Do you want to make this edit to .wyrm.toml?         │
│ ❯ 1. Yes                                             │
│   2. Yes, allow all edits during this session        │
│   3. No, and tell Claude what to do differently      │
╰──────────────────────────────────────────────────────╯
`

// blockedOptionsOnly exercises the weaker option-list shape on its own: a
// selector with no "do you want to"/"enter to confirm" text anywhere.
const blockedOptionsOnly = `
⏺ Which approach should I take?
   1. Rewrite the parser
   2. Patch the existing one
`

// busyWithProse is the false positive the option-list rule has to survive: the
// agent printed a numbered list *while working*. The live busy hint outranks it.
const busyWithProse = `
⏺ Here's the plan:
   1. Extract the geometry helper
   2. Add the hit test
   3. Wire up the menu
✻ Cogitating… (12s · ↑ 300 tokens)
────────────────────────────────────────────────────────
❯
────────────────────────────────────────────────────────
 Esc to cancel · Tab to amend
`

func TestDetect(t *testing.T) {
	tests := []struct {
		name    string
		command string
		content string
		want    State
	}{
		{"idle at a fresh prompt", "claude", idleFresh, StateIdle},
		{"idle after finishing a turn", "claude", idleDone, StateIdle},
		{"busy with a live elapsed counter", "claude", busySpinner, StateBusy},
		{"busy showing cancel/amend hints", "claude", busyHints, StateBusy},
		{"busy on a long run, counter in minutes", "claude", busyLongRun, StateBusy},
		{"blocked on the trust prompt", "claude", blockedTrust, StateBlocked},
		{"blocked on a permission request", "claude", blockedPermission, StateBlocked},
		{"blocked on a bare option list", "claude", blockedOptionsOnly, StateBlocked},
		{"busy beats a numbered list in prose", "claude", busyWithProse, StateBusy},
		{"a shell pane is not an agent", "zsh", idleFresh, StateNone},
		{"an empty pane still counts as idle", "claude", "", StateIdle},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Detect(tt.command, tt.content, nil); got != tt.want {
				t.Errorf("Detect(%q) = %v, want %v", tt.command, got, tt.want)
			}
		})
	}
}

// A prompt sitting under ordinary finished transcript still reads as blocked:
// the selector, not the surrounding text, is what makes the state.
func TestDetectBlockedUnderTranscript(t *testing.T) {
	content := "⏺ Read 3 files.\n  ⎿ done\n" + blockedPermission
	if got := Detect("claude", content, nil); got != StateBlocked {
		t.Errorf("Detect = %v, want StateBlocked", got)
	}
}

// The false positive that shaped blockedMarkers: an agent working on code that
// merely *talks about* prompts is busy, not blocked. Detection reads the
// agent's own chrome, never prose.
func TestDetectIgnoresPromptTextInTranscript(t *testing.T) {
	content := `
⏺ Update(internal/agent/agent_test.go)
  ⎿  Added 1 line
      143  {"blocked on a permission request", blockedPermission, StateBlocked},
      144  // "Do you want to make this edit to .wyrm.toml?"
      145  // Would you like to proceed with the plan?
✻ Cogitating… (2m 11s · ↓ 40k tokens)
────────────────────────────────────────────────────────
❯
`
	if got := Detect("claude", content, nil); got != StateBusy {
		t.Errorf("Detect = %v, want StateBusy — prompt text in a diff is not a prompt", got)
	}
}

// Even with no spinner on screen, quoted prompt prose must not read as blocked;
// without a selector there is nothing to answer.
func TestDetectPromptProseAloneIsNotBlocked(t *testing.T) {
	content := `
⏺ The permission flow asks "Do you want to proceed?" before each edit.
  Would you like to change that behaviour?
────────────────────────────────────────────────────────
❯
────────────────────────────────────────────────────────
  -- INSERT --
`
	if got := Detect("claude", content, nil); got != StateIdle {
		t.Errorf("Detect = %v, want StateIdle", got)
	}
}

// Only the bottom of the pane is examined: markers scrolled far enough up are
// history, not state.
func TestDetectIgnoresStaleScrollback(t *testing.T) {
	var padding string
	for i := 0; i < busyTail*2; i++ {
		padding += "⏺ some earlier output line\n"
	}
	content := blockedPermission + padding + idleFresh
	if got := Detect("claude", content, nil); got != StateIdle {
		t.Errorf("Detect = %v, want StateIdle (old prompt is out of the window)", got)
	}
}

func TestIsAgent(t *testing.T) {
	tests := []struct {
		command  string
		commands []string
		want     bool
	}{
		{"claude", nil, true},
		{"Claude", nil, true},
		{"/usr/local/bin/claude", nil, true},
		{"zsh", nil, false},
		{"node", nil, false},
		{"", nil, false},
		{"node", []string{"node"}, true},
		{"claude", []string{"aider"}, false},
		{"aider", []string{" aider ", "codex"}, true},
	}
	for _, tt := range tests {
		if got := IsAgent(tt.command, tt.commands); got != tt.want {
			t.Errorf("IsAgent(%q, %v) = %v, want %v", tt.command, tt.commands, got, tt.want)
		}
	}
}

func TestMergeTakesTheMostUrgent(t *testing.T) {
	tests := []struct {
		a, b, want State
	}{
		{StateNone, StateBusy, StateBusy},
		{StateBusy, StateIdle, StateIdle},
		{StateIdle, StateBlocked, StateBlocked},
		{StateBlocked, StateIdle, StateBlocked},
		{StateBlocked, StateBusy, StateBlocked},
		{StateNone, StateNone, StateNone},
	}
	for _, tt := range tests {
		if got := Merge(tt.a, tt.b); got != tt.want {
			t.Errorf("Merge(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestNeedsUser(t *testing.T) {
	for state, want := range map[State]bool{
		StateNone:    false,
		StateBusy:    false,
		StateBlocked: true,
		StateIdle:    true,
	} {
		if got := state.NeedsUser(); got != want {
			t.Errorf("%v.NeedsUser() = %v, want %v", state, got, want)
		}
	}
}
