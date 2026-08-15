package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

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

	appWatchCtx = ctx
	defer func() { appWatchCtx = nil }()

	var stdout, stderr bytes.Buffer
	code := run([]string{"status", "-format", "waybar", "--watch", "--interval", "10ms"}, &stdout, &stderr, r, func() bool { return false }, nil)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, `"class":"blocked"`) {
		t.Errorf("expected waybar json output with blocked class in watch mode, got:\n%s", out)
	}
}
