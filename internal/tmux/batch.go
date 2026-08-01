package tmux

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// BatchRunner is implemented by a Runner that can issue several tmux commands
// in one process. It is deliberately separate from Runner: callers reach it
// through RunEach/RunBatch, which fall back to one call per command, so mocks
// and the dry-run recorder need not implement it.
//
// DryRun in particular must *not* implement it — `wyrm up -n` prints one line
// per tmux command, and that transcript is the feature.
type BatchRunner interface {
	// RunBatch issues cmds in a single tmux process and returns the output of
	// each command that completed, in order. tmux stops a batch at its first
	// failure, so a result shorter than cmds says exactly where it stopped:
	// len(results) commands succeeded, and cmds[len(results)] is the one that
	// failed.
	RunBatch(cmds [][]string) ([]string, error)
}

// batchSep is the argument tmux reads as "end of this command". It is passed as
// its own argv element, so no shell is involved and no quoting applies.
const batchSep = ";"

// newBatchMarker returns the token printed after each command in a batch, so
// the caller can count how many ran. It is randomized per batch because the
// output being delimited includes user-controlled text — a session or window
// name could otherwise contain a fixed marker and shift every result by one.
func newBatchMarker() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Any token is better than none here; a collision only mis-splits
		// output that a caller is about to discard as failed anyway.
		return "wyrm-batch-marker"
	}
	return "wyrm-batch-" + hex.EncodeToString(b[:])
}

// RunBatch implements BatchRunner for the real tmux.
//
// It captures stdout and stderr separately rather than going through Run, which
// substitutes stderr for stdout on failure: a partly-completed batch needs both
// — the output to count what ran, and the diagnostic to say why it stopped.
func (e Exec) RunBatch(cmds [][]string) ([]string, error) {
	if len(cmds) == 0 {
		return nil, nil
	}

	marker := newBatchMarker()
	var args []string
	if e.SocketName != "" {
		args = append(args, "-L", e.SocketName)
	}
	for i, c := range cmds {
		if i > 0 {
			args = append(args, batchSep)
		}
		args = append(args, c...)
		// A marker after every command, so a command that prints nothing
		// (send-keys) is still countable.
		args = append(args, batchSep, "display-message", "-p", marker)
	}

	cmd := exec.Command("tmux", args...)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()

	results := splitOnMarker(stdout.String(), marker)
	if runErr == nil {
		return results, nil
	}

	msg := strings.TrimSpace(stderr.String())
	if noServerText(msg) {
		return results, fmt.Errorf("%w: %w", ErrNoServer, runErr)
	}
	if msg == "" {
		return results, runErr
	}
	return results, fmt.Errorf("%w (%s)", runErr, msg)
}

// splitOnMarker cuts batch output into one entry per completed command. Lines
// before the first marker belong to the first command, and so on; the trailing
// text after the last marker belongs to a command that never finished and is
// dropped.
func splitOnMarker(out, marker string) []string {
	if out == "" {
		return nil
	}
	var results []string
	var cur []string
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimRight(line, "\r") == marker {
			results = append(results, strings.TrimSpace(strings.Join(cur, "\n")))
			cur = cur[:0]
			continue
		}
		cur = append(cur, line)
	}
	return results
}

// RunEach issues every command in cmds and returns a slice of errors aligned
// with it: nil where the command succeeded.
//
// It uses one tmux process when the Runner supports batching, and one per
// command when it doesn't. Because tmux abandons a batch at its first failure,
// the commands it never reached are replayed individually — otherwise a single
// broken command would silently cancel every command after it, which is not
// internal/session's documented "warn and continue" policy.
//
// Commands that already succeeded are never re-issued. That matters: replaying
// a send-keys would type its text into the pane a second time, which is a worse
// outcome than the failure being recovered from. This relies on a failed tmux
// command having had no effect, which holds for the commands wyrm batches
// (send-keys, capture-pane) — they fail by not finding their target.
func RunEach(r Runner, cmds [][]string) []error {
	errs := make([]error, len(cmds))
	if len(cmds) == 0 {
		return errs
	}

	start := 0
	if b, ok := r.(BatchRunner); ok {
		results, err := b.RunBatch(cmds)
		if err == nil && len(results) >= len(cmds) {
			return errs
		}
		// len(results) commands ran; the next one is where it stopped.
		start = min(len(results), len(cmds))
	}

	for i := start; i < len(cmds); i++ {
		if out, err := r.Run(cmds[i]...); err != nil {
			errs[i] = batchCmdErr(err, out)
		}
	}
	return errs
}

// RunOutputsTolerant issues every command and returns each one's output —
// empty where it failed — alongside the matching errors, both aligned with
// cmds.
//
// It is RunEach for reads: one tmux process when the Runner batches, with the
// commands a failure cut short replayed individually so one dead target doesn't
// discard every result after it. That is the case it exists for — the TUI's
// agent scan captures a set of panes, any of which may have closed since the
// list that named them.
func RunOutputsTolerant(r Runner, cmds [][]string) ([]string, []error) {
	outs := make([]string, len(cmds))
	errs := make([]error, len(cmds))
	if len(cmds) == 0 {
		return outs, errs
	}

	start := 0
	if b, ok := r.(BatchRunner); ok {
		results, err := b.RunBatch(cmds)
		copy(outs, results)
		if err == nil && len(results) >= len(cmds) {
			return outs, errs
		}
		start = min(len(results), len(cmds))
	}

	for i := start; i < len(cmds); i++ {
		out, err := r.Run(cmds[i]...)
		if err != nil {
			errs[i] = batchCmdErr(err, out)
			continue
		}
		outs[i] = out
	}
	return outs, errs
}

func batchCmdErr(err error, out string) error {
	if out == "" {
		return err
	}
	if errors.Is(err, ErrNoServer) {
		return err
	}
	return fmt.Errorf("%w (%s)", err, out)
}
