package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jskoll/wyrm/internal/agent"
)

// agentMaxCapturesForTest mirrors the production bound, named locally so a
// change to it shows up here as a deliberate edit rather than a silent shift.
const agentMaxCapturesForTest = agent.MaxCaptures

type statusFakeRunner struct {
	panes   string
	capture map[string]string
}

func (s *statusFakeRunner) Run(args ...string) (string, error) {
	if len(args) == 0 {
		return "", nil
	}
	if args[0] == "list-panes" {
		return s.panes, nil
	}
	if args[0] == "capture-pane" {
		for i, a := range args {
			if a == "-t" && i+1 < len(args) {
				return s.capture[args[i+1]], nil
			}
		}
	}
	return "", nil
}

func TestRunStatusFormats(t *testing.T) {
	r := &statusFakeRunner{
		panes: "$1\x01myproj\x01@1\x011\x01win1\x01%1\x011\x01claude",
		capture: map[string]string{
			"%1": "something\nenter to confirm", // blocked state
		},
	}

	for _, format := range []string{"text", "json", "tmux", "waybar", "sketchybar"} {
		t.Run(format, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run([]string{"status", "-format", format}, &stdout, &stderr, r, func() bool { return false }, nil)
			if code != 0 {
				t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
			}
			out := stdout.String()
			if out == "" {
				t.Errorf("expected non-empty output for format %s", format)
			}
		})
	}
}

func TestRunStatusWatchMode(t *testing.T) {
	r := &statusFakeRunner{
		panes: "$1\x01myproj\x01@1\x011\x01win1\x01%1\x011\x01claude",
		capture: map[string]string{
			"%1": "something\nenter to confirm",
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	var stdout, stderr bytes.Buffer
	code := runWith(runOptions{watchCtx: ctx},
		[]string{"status", "-format", "waybar", "--watch", "--interval", "10ms"}, &stdout, &stderr, r, func() bool { return false }, nil)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, `"class":"blocked"`) {
		t.Errorf("expected waybar json output with blocked class in watch mode, got:\n%s", out)
	}
}

// TestRunStatusWatchInvalidFormatFails is the regression test for --watch
// silently discarding every iteration's collection/format error: a typo'd
// -format used to loop forever printing nothing, with a zero exit code,
// until interrupted, instead of failing the way it does without -watch.
func TestRunStatusWatchInvalidFormatFails(t *testing.T) {
	r := &statusFakeRunner{
		panes: "$1\x01myproj\x01@1\x011\x01win1\x01%1\x011\x01claude",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	var stdout, stderr bytes.Buffer
	code := runWith(runOptions{watchCtx: ctx},
		[]string{"status", "-format", "not-a-real-format", "--watch", "--interval", "10ms"}, &stdout, &stderr, r, func() bool { return false }, nil)
	if code == 0 {
		t.Fatalf("exit code = 0, want a nonzero exit for an invalid -format; stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "unknown format") {
		t.Errorf("stderr = %q, want an unknown-format error", stderr.String())
	}
}

// countingStatusRunner records how many capture-pane calls it served, so the
// scan bound can be asserted on behaviour rather than on a constant.
type countingStatusRunner struct {
	statusFakeRunner
	captures int
}

func (c *countingStatusRunner) Run(args ...string) (string, error) {
	if len(args) > 0 && args[0] == "capture-pane" {
		c.captures++
	}
	return c.statusFakeRunner.Run(args...)
}

// `wyrm status --watch` issued one capture-pane per agent pane, every interval,
// with no bound — the tmux call storm the TUI's cap was introduced to prevent,
// against the server the user is working in. The bound is now shared, and a
// truncated scan says so instead of quietly under-reporting.
func TestStatusBoundsCapturesAndReportsTheRemainder(t *testing.T) {
	const panes = agentMaxCapturesForTest + 9

	var b strings.Builder
	capture := map[string]string{}
	for i := 1; i <= panes; i++ {
		id := fmt.Sprintf("%%%d", i)
		fmt.Fprintf(&b, "$1\x01myproj\x01@1\x011\x01win1\x01%s\x01%d\x01claude\n", id, i)
		capture[id] = "something\nenter to confirm"
	}
	r := &countingStatusRunner{statusFakeRunner: statusFakeRunner{panes: b.String(), capture: capture}}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"status", "-format", "json"}, &stdout, &stderr, r, func() bool { return false }, nil); code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}

	if r.captures > agentMaxCapturesForTest {
		t.Errorf("captured %d panes, want at most %d", r.captures, agentMaxCapturesForTest)
	}

	var report agentStatusReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decoding status json: %v\n%s", err, stdout.String())
	}
	if report.Summary.Skipped != panes-agentMaxCapturesForTest {
		t.Errorf("summary.skipped = %d, want %d", report.Summary.Skipped, panes-agentMaxCapturesForTest)
	}
	if report.Summary.Total != agentMaxCapturesForTest {
		t.Errorf("summary.total = %d, want the %d that were actually scanned",
			report.Summary.Total, agentMaxCapturesForTest)
	}
}

// A scan that fits under the bound reports nothing skipped, and the field stays
// out of the JSON entirely.
func TestStatusReportsNothingSkippedWhenUnderTheBound(t *testing.T) {
	r := &statusFakeRunner{
		panes:   "$1\x01myproj\x01@1\x011\x01win1\x01%1\x011\x01claude",
		capture: map[string]string{"%1": "something\nenter to confirm"},
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"status", "-format", "json"}, &stdout, &stderr, r, func() bool { return false }, nil); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if strings.Contains(stdout.String(), "skipped") {
		t.Errorf("an unbounded scan should not mention skipping:\n%s", stdout.String())
	}
}

// A truncated scan has to say so even when nothing else is worth printing.
// Every scanned agent being busy produces no blocked/idle parts at all, and
// that is exactly when the unscanned panes matter most: one of the ones that
// went unread could be the one waiting on an answer.
func TestStatusReportsTruncationWhenEveryScannedAgentIsBusy(t *testing.T) {
	const panes = agentMaxCapturesForTest + 4

	var b strings.Builder
	capture := map[string]string{}
	for i := 1; i <= panes; i++ {
		id := fmt.Sprintf("%%%d", i)
		fmt.Fprintf(&b, "$1\x01myproj\x01@1\x011\x01win1\x01%s\x01%d\x01claude\n", id, i)
		capture[id] = "working on it\nesc to interrupt"
	}
	r := &statusFakeRunner{panes: b.String(), capture: capture}

	for _, format := range []string{"text", "tmux"} {
		t.Run(format, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run([]string{"status", "-format", format}, &stdout, &stderr, r,
				func() bool { return false }, nil); code != 0 {
				t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
			}
			out := stdout.String()
			if !strings.Contains(out, "unscanned") {
				t.Errorf("%s: truncation went unreported with every agent busy, got %q", format, out)
			}
			if !strings.Contains(out, fmt.Sprintf("%d", panes-agentMaxCapturesForTest)) {
				t.Errorf("%s: want the skipped count, got %q", format, out)
			}
		})
	}
}

// Nothing running at all still prints nothing — the indicator must not turn a
// quiet status bar into a noisy one.
func TestStatusStaysQuietWithNoAgents(t *testing.T) {
	r := &statusFakeRunner{panes: "$1\x01myproj\x01@1\x011\x01win1\x01%1\x011\x01bash", capture: map[string]string{}}
	for _, format := range []string{"text", "tmux"} {
		var stdout, stderr bytes.Buffer
		if code := run([]string{"status", "-format", format}, &stdout, &stderr, r,
			func() bool { return false }, nil); code != 0 {
			t.Fatalf("exit code = %d", code)
		}
		if out := stdout.String(); strings.TrimSpace(out) != "" {
			t.Errorf("%s: want no output with no agents, got %q", format, out)
		}
	}
}
