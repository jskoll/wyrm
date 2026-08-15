package agent

import (
	"bytes"
	"strings"
	"testing"
)

func TestNotificationFormatting(t *testing.T) {
	n := Notification{
		State:       StateBlocked,
		PaneID:      "%1",
		SessionName: "myproj",
		WindowName:  "code",
	}

	if got := n.FormattedTitle(); got != "Wyrm Agent: myproj" {
		t.Errorf("FormattedTitle = %q, want %q", got, "Wyrm Agent: myproj")
	}

	msg := n.FormattedMessage()
	if !strings.Contains(msg, "needs confirmation or input") || !strings.Contains(msg, "code (%1)") {
		t.Errorf("unexpected FormattedMessage: %q", msg)
	}

	nIdle := Notification{
		State:       StateIdle,
		PaneID:      "%2",
		SessionName: "",
		WindowName:  "",
	}
	if got := nIdle.FormattedTitle(); got != "Wyrm Agent Alert" {
		t.Errorf("FormattedTitle = %q, want %q", got, "Wyrm Agent Alert")
	}
	if got := nIdle.FormattedMessage(); !strings.Contains(got, "finished its turn") {
		t.Errorf("unexpected FormattedMessage: %q", got)
	}
}

func TestDispatchBellAndOSC(t *testing.T) {
	var buf bytes.Buffer
	n := Notification{
		State:       StateBlocked,
		PaneID:      "%1",
		SessionName: "test-sess",
		WindowName:  "w0",
	}

	cfg := NotifyConfig{
		Enabled:   true,
		Bell:      true,
		OSC:       true,
		OnBlocked: true,
	}

	if err := Dispatch(n, cfg, &buf); err != nil {
		t.Fatalf("Dispatch failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "\a") {
		t.Errorf("expected bell in output: %q", out)
	}
	if !strings.Contains(out, "\x1b]777;notify;Wyrm Agent: test-sess;") {
		t.Errorf("expected OSC 777 in output: %q", out)
	}
	if !strings.Contains(out, "\x1b]9;") {
		t.Errorf("expected OSC 9 in output: %q", out)
	}
}

func TestDispatchDisabled(t *testing.T) {
	var buf bytes.Buffer
	n := Notification{State: StateBlocked}
	cfg := NotifyConfig{Enabled: false, Bell: true}
	if err := Dispatch(n, cfg, &buf); err != nil {
		t.Fatalf("Dispatch failed: %v", err)
	}
	if buf.Len() > 0 {
		t.Errorf("expected no output when disabled, got %q", buf.String())
	}
}

func TestDispatchFiltering(t *testing.T) {
	var buf bytes.Buffer
	nBlocked := Notification{State: StateBlocked}
	cfgNoBlocked := NotifyConfig{Enabled: true, Bell: true, OnBlocked: false}
	_ = Dispatch(nBlocked, cfgNoBlocked, &buf)
	if buf.Len() > 0 {
		t.Errorf("expected no output when OnBlocked is false, got %q", buf.String())
	}

	nIdle := Notification{State: StateIdle}
	cfgNoIdle := NotifyConfig{Enabled: true, Bell: true, OnIdle: false}
	_ = Dispatch(nIdle, cfgNoIdle, &buf)
	if buf.Len() > 0 {
		t.Errorf("expected no output when OnIdle is false, got %q", buf.String())
	}
}

func TestDispatchDesktop(t *testing.T) {
	var desktopTitle, desktopMsg string
	oldSend := SendDesktopNotification
	defer func() { SendDesktopNotification = oldSend }()
	SendDesktopNotification = func(title, msg string) {
		desktopTitle = title
		desktopMsg = msg
	}

	n := Notification{
		State:       StateBlocked,
		PaneID:      "%5",
		SessionName: "proj",
		WindowName:  "dev",
	}
	cfg := NotifyConfig{
		Enabled:   true,
		Desktop:   true,
		OnBlocked: true,
	}

	if err := Dispatch(n, cfg, nil); err != nil {
		t.Fatalf("Dispatch failed: %v", err)
	}

	if desktopTitle != "Wyrm Agent: proj" {
		t.Errorf("desktopTitle = %q, want %q", desktopTitle, "Wyrm Agent: proj")
	}
	if !strings.Contains(desktopMsg, "dev (%5)") {
		t.Errorf("desktopMsg = %q, want mentioning dev (%%5)", desktopMsg)
	}
}
