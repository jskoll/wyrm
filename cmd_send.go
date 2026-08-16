package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/jskoll/wyrm/internal/tmux"
)

// send transmits commands or keystrokes non-interactively to a target session,
// window, or pane.
func (a *app) send(args []string) error {
	fs := a.newFlagSet("send")
	literal := fs.Bool("l", false, "send exact characters (disable special key expansion)")
	fs.BoolVar(literal, "literal", false, "send exact characters (disable special key expansion)")
	noEnter := fs.Bool("n", false, "do not append Enter keystroke")
	fs.BoolVar(noEnter, "no-enter", false, "do not append Enter keystroke")
	raw := fs.Bool("r", false, "send raw tmux key symbols (e.g. C-c, Escape)")
	fs.BoolVar(raw, "raw", false, "send raw tmux key symbols (e.g. C-c, Escape)")

	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return usageErrf("target required (usage: wyrm send [flags] <session[:window[.pane]]> <command...>)")
	}
	if fs.NArg() < 2 {
		return usageErrf("command or keys required to send to %q", fs.Arg(0))
	}

	target := fs.Arg(0)
	targetID, err := resolveSendTarget(a.runner, target)
	if err != nil {
		return err
	}

	if *raw {
		cmdArgs := append([]string{"send-keys", "-t", targetID}, fs.Args()[1:]...)
		if !*noEnter {
			cmdArgs = append(cmdArgs, "Enter")
		}
		out, err := a.runner.Run(cmdArgs...)
		if err != nil {
			return fmt.Errorf("sending keys to %q (%s): %w", target, targetID, tmux.CmdErr(err, out))
		}
		return nil
	}

	text := strings.Join(fs.Args()[1:], " ")
	out, err := a.runner.Run("send-keys", "-t", targetID, "-l", text)
	if err != nil {
		return fmt.Errorf("sending keys to %q (%s): %w", target, targetID, tmux.CmdErr(err, out))
	}
	if !*noEnter {
		if out, err := a.runner.Run("send-keys", "-t", targetID, "Enter"); err != nil {
			return fmt.Errorf("sending Enter to %q (%s): %w", target, targetID, tmux.CmdErr(err, out))
		}
	}
	return nil
}

// resolveSendTarget resolves a user-supplied target string (e.g. "myproj",
// "myproj:editor", "myproj:editor.1", "%3", "@1", "$1") into a safe tmux target ID.
func resolveSendTarget(r tmux.Runner, target string) (string, error) {
	if strings.HasPrefix(target, "%") || strings.HasPrefix(target, "@") || strings.HasPrefix(target, "$") {
		return target, nil
	}

	sessPart, rest, hasColon := strings.Cut(target, ":")
	sessName := sessPart
	winName := ""
	paneSpec := ""
	if hasColon {
		winPart, panePart, hasDot := strings.Cut(rest, ".")
		if hasDot {
			winName = winPart
			paneSpec = panePart
		} else {
			winName = rest
		}
	}

	sessID, ok, err := tmux.FindSessionID(r, sessName)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("no running session named %q", sessName)
	}
	if winName == "" && paneSpec == "" {
		return sessID, nil
	}

	windows, err := tmux.ListWindows(r, sessID)
	if err != nil {
		return "", fmt.Errorf("listing windows for session %q: %w", sessName, err)
	}

	var targetWin *tmux.WindowInfo
	for i := range windows {
		w := &windows[i]
		if w.Name == winName || strings.EqualFold(w.Name, winName) || w.ID == winName {
			targetWin = w
			break
		}
		if idx, err := strconv.Atoi(winName); err == nil && w.Index == idx {
			targetWin = w
			break
		}
	}
	if targetWin == nil {
		return "", fmt.Errorf("no window matching %q in session %q", winName, sessName)
	}
	if paneSpec == "" {
		return targetWin.ID, nil
	}

	panes, err := tmux.ListPanes(r, targetWin.ID)
	if err != nil {
		return "", fmt.Errorf("listing panes for window %q: %w", winName, err)
	}

	var targetPane *tmux.PaneInfo
	for i := range panes {
		p := &panes[i]
		if p.ID == paneSpec || p.Command == paneSpec {
			targetPane = p
			break
		}
		if idx, err := strconv.Atoi(paneSpec); err == nil && p.Index == idx {
			targetPane = p
			break
		}
	}
	if targetPane == nil {
		return "", fmt.Errorf("no pane matching %q in window %q of session %q", paneSpec, winName, sessName)
	}
	return targetPane.ID, nil
}
