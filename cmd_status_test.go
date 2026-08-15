package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestStatusCmdTextOutput(t *testing.T) {
	mockRunner := &statusTestRunner{
		panes: []string{"$1\x01backend\x01@1\x010\x01code\x01%1\x010\x01claude"},
		paneOutputs: map[string]string{
			"%1": "Allow Claude to execute `npm test`?\n  1. Yes\n  2. No\nChoose: ",
		},
	}
	var stdout, stderr bytes.Buffer
	app := &app{
		stdout: &stdout,
		stderr: &stderr,
		runner: mockRunner,
	}

	if err := app.status([]string{"-format", "text"}); err != nil {
		t.Fatalf("status failed: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "⏸ 1 blocked") {
		t.Errorf("expected text output to contain '⏸ 1 blocked', got %q", out)
	}

	// Test verbose text
	stdout.Reset()
	if err := app.status([]string{"-v"}); err != nil {
		t.Fatalf("status -v failed: %v", err)
	}
	vOut := stdout.String()
	if !strings.Contains(vOut, "backend: @0:code %1 (claude) - blocked") {
		t.Errorf("expected verbose output to contain pane info, got %q", vOut)
	}
}

func TestStatusCmdJsonOutput(t *testing.T) {
	mockRunner := &statusTestRunner{
		panes: []string{"$1\x01backend\x01@1\x010\x01code\x01%1\x010\x01claude"},
		paneOutputs: map[string]string{
			"%1": "Allow Claude to execute `npm test`?\n  1. Yes\n  2. No\nChoose: ",
		},
	}
	var stdout, stderr bytes.Buffer
	app := &app{
		stdout: &stdout,
		stderr: &stderr,
		runner: mockRunner,
	}

	if err := app.status([]string{"-format", "json"}); err != nil {
		t.Fatalf("status -format json failed: %v", err)
	}

	var report agentStatusReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("failed to parse json output: %v", err)
	}
	if report.Summary.Blocked != 1 {
		t.Errorf("report.Summary.Blocked = %d, want 1", report.Summary.Blocked)
	}
	if len(report.Agents) != 1 || report.Agents[0].State != "blocked" {
		t.Errorf("unexpected report.Agents: %+v", report.Agents)
	}
}

func TestStatusCmdTmuxWaybarSketchybar(t *testing.T) {
	mockRunner := &statusTestRunner{
		panes: []string{"$1\x01backend\x01@1\x010\x01code\x01%1\x010\x01claude"},
		paneOutputs: map[string]string{
			"%1": "Allow Claude to execute `npm test`?\n  1. Yes\n  2. No\nChoose: ",
		},
	}
	var stdout, stderr bytes.Buffer
	app := &app{
		stdout: &stdout,
		stderr: &stderr,
		runner: mockRunner,
	}

	// tmux format
	if err := app.status([]string{"-format", "tmux"}); err != nil {
		t.Fatalf("status -format tmux failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "#[fg=yellow,bold]⏸ 1 blocked#[default]") {
		t.Errorf("unexpected tmux output: %q", stdout.String())
	}

	// waybar format
	stdout.Reset()
	if err := app.status([]string{"-format", "waybar"}); err != nil {
		t.Fatalf("status -format waybar failed: %v", err)
	}
	if !strings.Contains(stdout.String(), `"class":"blocked"`) {
		t.Errorf("unexpected waybar output: %q", stdout.String())
	}

	// sketchybar format
	stdout.Reset()
	if err := app.status([]string{"-format", "sketchybar"}); err != nil {
		t.Fatalf("status -format sketchybar failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "icon=⏸ label=\"1 blocked\" drawing=on") {
		t.Errorf("unexpected sketchybar output: %q", stdout.String())
	}
}

type statusTestRunner struct {
	panes       []string
	paneOutputs map[string]string
}

func (s *statusTestRunner) Run(args ...string) (string, error) {
	if len(args) > 0 && args[0] == "list-panes" && args[1] == "-a" {
		return strings.Join(s.panes, "\n"), nil
	}
	if len(args) >= 4 && args[0] == "capture-pane" {
		target := args[3]
		return s.paneOutputs[target], nil
	}
	return "", nil
}
