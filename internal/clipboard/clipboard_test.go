package clipboard

import (
	"bytes"
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
