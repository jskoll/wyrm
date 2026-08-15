// Package clipboard writes text to the system clipboard and formats OSC 52 sequences.
package clipboard

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
)

// OSC52 formats text as an OSC 52 clipboard escape sequence.
func OSC52(text string) string {
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	if os.Getenv("TMUX") != "" {
		// In tmux, wrap in DCS passthrough
		return fmt.Sprintf("\x1bPtmux;\x1b\x1b]52;c;%s\x07\x1b\\", encoded)
	}
	return fmt.Sprintf("\x1b]52;c;%s\x07", encoded)
}

// Write attempts to copy text to the system clipboard using platform tools.
func Write(text string) error {
	return writeSystemClipboard(text)
}

// WriteWithOSC copies to system clipboard and also writes OSC 52 to w if non-nil.
func WriteWithOSC(text string, w io.Writer) error {
	if w != nil {
		_, _ = fmt.Fprint(w, OSC52(text))
	}
	return Write(text)
}

var writeSystemClipboard = func(text string) error {
	switch runtime.GOOS {
	case "darwin":
		cmd := exec.Command("pbcopy")
		cmd.Stdin = stringsReader(text)
		return cmd.Run()
	case "linux", "freebsd", "openbsd", "netbsd":
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			if _, err := exec.LookPath("wl-copy"); err == nil {
				cmd := exec.Command("wl-copy")
				cmd.Stdin = stringsReader(text)
				return cmd.Run()
			}
		}
		if _, err := exec.LookPath("xclip"); err == nil {
			cmd := exec.Command("xclip", "-selection", "clipboard")
			cmd.Stdin = stringsReader(text)
			return cmd.Run()
		}
		if _, err := exec.LookPath("xsel"); err == nil {
			cmd := exec.Command("xsel", "--clipboard", "--input")
			cmd.Stdin = stringsReader(text)
			return cmd.Run()
		}
	case "windows":
		cmd := exec.Command("powershell", "-NoProfile", "-Command", "Set-Clipboard -Value $input")
		cmd.Stdin = stringsReader(text)
		return cmd.Run()
	}
	return nil
}

func stringsReader(s string) io.Reader {
	return &stringReader{s: s}
}

type stringReader struct {
	s   string
	pos int
}

func (r *stringReader) Read(p []byte) (n int, err error) {
	if r.pos >= len(r.s) {
		return 0, io.EOF
	}
	n = copy(p, r.s[r.pos:])
	r.pos += n
	return n, nil
}
