package clipboard

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestOSC52(t *testing.T) {
	seq := OSC52("hello")
	if !strings.Contains(seq, "aGVsbG8=") {
		t.Errorf("expected base64 encoded 'hello' in OSC52 sequence: %q", seq)
	}
}

func TestWriteWithOSC(t *testing.T) {
	var buf bytes.Buffer
	oldWrite := writeSystemClipboard
	defer func() { writeSystemClipboard = oldWrite }()
	var copiedText string
	writeSystemClipboard = func(text string) error {
		copiedText = text
		return nil
	}

	if err := WriteWithOSC("sample text", &buf); err != nil {
		t.Fatalf("WriteWithOSC failed: %v", err)
	}
	if copiedText != "sample text" {
		t.Errorf("copiedText = %q, want 'sample text'", copiedText)
	}
	if !strings.Contains(buf.String(), "\x1b]52;c;") {
		t.Errorf("expected OSC 52 sequence in buffer: %q", buf.String())
	}
}

// A platform with no clipboard tool must report that, not return nil. Every
// caller announced "copied ..." on the strength of a nil error, so on a
// headless box "y" claimed success and did nothing.
func TestWriteReportsMissingBackend(t *testing.T) {
	prev := writeSystemClipboard
	writeSystemClipboard = func(string) error { return ErrNoBackend }
	t.Cleanup(func() { writeSystemClipboard = prev })

	if err := Write("hello"); !errors.Is(err, ErrNoBackend) {
		t.Errorf("Write = %v, want ErrNoBackend", err)
	}
	if Backends() == "" {
		t.Error("Backends() should name the tools to install")
	}
}
