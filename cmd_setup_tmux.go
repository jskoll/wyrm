package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// setupTmux generates or appends recommended tmux keybindings and popup configurations.
func (a *app) setupTmux(args []string) error {
	fs := a.newFlagSet("setup-tmux")
	appendConf := fs.Bool("a", false, "append configuration directly to tmux.conf")
	fs.BoolVar(appendConf, "append", false, "append configuration directly to tmux.conf")
	fs.BoolVar(appendConf, "write", false, "append configuration directly to tmux.conf")
	fs.BoolVar(appendConf, "w", false, "append configuration directly to tmux.conf")
	keyPick := fs.String("key-pick", "C-j", "prefix key combination to open wyrm pick popup")
	keyTUI := fs.String("key-tui", "C-w", "prefix key combination to open wyrm tui popup")
	status := fs.Bool("status", true, "include wyrm status bar integration")

	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if err := requireNoArgs(fs); err != nil {
		return err
	}

	snippet := generateTmuxSnippet(*keyPick, *keyTUI, *status)

	if !*appendConf {
		_, _ = fmt.Fprint(a.stdout, snippet)
		return nil
	}

	targetPath := findTmuxConfPath()
	return appendTmuxConf(targetPath, snippet, a.stdout)
}

func generateTmuxSnippet(keyPick, keyTUI string, status bool) string {
	var b strings.Builder
	b.WriteString("# --- wyrm tmux integration ---\n")
	fmt.Fprintf(&b, "# Float wyrm session picker with Prefix + %s\n", keyPick)
	fmt.Fprintf(&b, "bind-key %s display-popup -E -w 80%% -h 80%% \"wyrm pick\"\n\n", keyPick)
	fmt.Fprintf(&b, "# Float full wyrm TUI session manager with Prefix + %s\n", keyTUI)
	fmt.Fprintf(&b, "bind-key %s display-popup -E -w 90%% -h 85%% \"wyrm tui\"\n", keyTUI)
	if status {
		b.WriteString("\n# Optional status bar agent indicator\n")
		b.WriteString("set -g status-right '#(wyrm status --format tmux) | %H:%M '\n")
	}
	b.WriteString("# --- end wyrm tmux integration ---\n")
	return b.String()
}

func findTmuxConfPath() string {
	if conf := os.Getenv("TMUX_CONF"); conf != "" {
		return conf
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".tmux.conf"
	}
	xdgPath := filepath.Join(home, ".config", "tmux", "tmux.conf")
	if _, err := os.Stat(xdgPath); err == nil {
		return xdgPath
	}
	return filepath.Join(home, ".tmux.conf")
}

func appendTmuxConf(path, snippet string, stdout io.Writer) error {
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	if bytes.Contains(existing, []byte("# --- wyrm tmux integration ---")) {
		_, _ = fmt.Fprintf(stdout, "wyrm tmux integration is already present in %s\n", path)
		return nil
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("opening %s for writing: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	if len(existing) > 0 && !bytes.HasSuffix(existing, []byte("\n")) {
		if _, err := f.WriteString("\n"); err != nil {
			return err
		}
	}
	if _, err := f.WriteString("\n" + snippet); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "appended wyrm integration to %s\n", path)
	return nil
}
