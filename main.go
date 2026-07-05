package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	Session SessionConfig `toml:"session"`
	Windows []Window      `toml:"windows"`
}

type SessionConfig struct {
	Name           string `toml:"name"`
	Root           string `toml:"root"`
	OnProjectStart string `toml:"on_project_start"`
	OnProjectExit  string `toml:"on_project_exit"`
	StartupWindow  string `toml:"startup_window"`
	StartupPane    *int   `toml:"startup_pane"` // Pointer allows nil (unset) vs 0 (select pane 0)
}

type Window struct {
	Name      string  `toml:"name"`
	Layout    string  `toml:"layout"`
	Splits    []Split `toml:"splits"`
	Panes     []Pane  `toml:"panes"`
	PreWindow string  `toml:"pre_window"`
}

type Split struct {
	Type     string  `toml:"type"`
	Size     int     `toml:"size"`
	Command  string  `toml:"command"`
	Children []Split `toml:"children"`
}

type Pane struct {
	Command string `toml:"command"`
}

// tmux runs a tmux command and returns its combined output, trimmed.
func tmux(args ...string) (string, error) {
	out, err := exec.Command("tmux", args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// sendKeys types a command into the target pane. Commands starting with "#"
// are treated as comments and skipped.
func sendKeys(target, command string) {
	if command == "" || strings.HasPrefix(command, "#") {
		return
	}
	if out, err := tmux("send-keys", "-t", target, command, "Enter"); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to run command '%s' in %s: %v\nOutput: %s\n", command, target, err, out)
	} else {
		fmt.Printf("  Running: %s\n", command)
	}
}

// resolveSession returns the session name and absolute root directory,
// deriving the name from the root's basename when name is unset.
func resolveSession(s SessionConfig) (string, string) {
	root := os.ExpandEnv(s.Root)
	absRoot, err := filepath.Abs(root)
	if err != nil {
		log.Fatalf("Invalid root path: %v", err)
	}
	name := s.Name
	if name == "" {
		name = filepath.Base(absRoot)
	}
	return name, absRoot
}

func runShellHook(hook string, workDir string) error {
	if hook == "" {
		return nil
	}
	cmd := exec.Command("bash", "-c", hook)
	cmd.Dir = workDir
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("hook failed: %v\nOutput: %s", err, output)
	}
	return nil
}

func main() {
	configFile := flag.String("config", ".tmuxconfig", "Path to .tmuxconfig file")
	kill := flag.Bool("kill", false, "Kill the session instead of creating it")
	flag.Parse()

	data, err := os.ReadFile(*configFile)
	if err != nil {
		log.Fatalf("Failed to read config: %v", err)
	}

	var config Config
	if err := toml.Unmarshal(data, &config); err != nil {
		log.Fatalf("Failed to parse config: %v", err)
	}

	if config.Session.Name == "" && config.Session.Root == "" {
		log.Fatal("Config must specify either session.name or session.root")
	}

	if *kill {
		killSession(config.Session)
		return
	}

	createSession(config.Session, config.Windows)
}

func createSession(s SessionConfig, windows []Window) {
	sessionName, absRoot := resolveSession(s)

	if len(windows) == 0 {
		log.Fatal("No windows defined in config")
	}

	// Run on_project_start hook before creating session
	if err := runShellHook(s.OnProjectStart, absRoot); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: on_project_start failed: %v\n", err)
	}

	// Kill existing session if it exists (wait a moment to ensure cleanup)
	tmux("kill-session", "-t", sessionName)
	time.Sleep(50 * time.Millisecond)

	// Create session with its first window; -P -F reports the window index
	// tmux actually assigned, so this works with any base-index setting.
	firstWindow := windows[0]
	windowIndex, err := tmux("new-session", "-d", "-P", "-F", "#{window_index}",
		"-s", sessionName, "-n", firstWindow.Name, "-c", absRoot)
	if err != nil {
		log.Fatalf("Failed to create session: %v\nOutput: %s", err, windowIndex)
	}

	fmt.Printf("Creating window %s: %s\n", windowIndex, firstWindow.Name)
	createPanes(sessionName+":"+windowIndex, firstWindow)

	// Create additional windows
	for _, window := range windows[1:] {
		windowIndex, err := tmux("new-window", "-P", "-F", "#{window_index}",
			"-t", sessionName, "-n", window.Name, "-c", absRoot)
		if err != nil {
			log.Fatalf("Failed to create window '%s': %v\nOutput: %s", window.Name, err, windowIndex)
		}
		fmt.Printf("Creating window %s: %s\n", windowIndex, window.Name)
		createPanes(sessionName+":"+windowIndex, window)
	}

	// Select startup window if specified
	if s.StartupWindow != "" {
		selectStartupWindow(sessionName, s.StartupWindow, s.StartupPane)
	}

	fmt.Printf("Created session: %s\n", sessionName)

	// Inside an existing tmux client, attach-session would nest — switch instead.
	if os.Getenv("TMUX") != "" {
		if out, err := tmux("switch-client", "-t", sessionName); err != nil {
			log.Fatalf("Failed to switch to session: %v\nOutput: %s", err, out)
		}
		return
	}

	attachCmd := exec.Command("tmux", "attach-session", "-t", sessionName)
	attachCmd.Stdin = os.Stdin
	attachCmd.Stdout = os.Stdout
	attachCmd.Stderr = os.Stderr
	if err := attachCmd.Run(); err != nil {
		log.Fatalf("Failed to attach to session: %v", err)
	}
}

func normalizeSplitType(t string) string {
	switch strings.ToLower(t) {
	case "h", "horizontal":
		return "h"
	default:
		return "v"
	}
}

func createPanes(windowID string, window Window) {
	// Allow tmux time to initialize window before sending commands
	time.Sleep(50 * time.Millisecond)

	if len(window.Splits) > 0 {
		createPanesFromSplits(windowID, window.Splits, window.PreWindow)
	} else if len(window.Panes) > 0 {
		createPanesFromList(windowID, window.Panes, window.Layout, window.PreWindow)
	} else if window.PreWindow != "" {
		sendKeys(windowID, window.PreWindow)
	}
}

func createPanesFromSplits(windowID string, splits []Split, preWindow string) {
	for paneIdx, split := range splits {
		targetPane := windowID
		if paneIdx > 0 {
			targetPane = fmt.Sprintf("%s.%d", windowID, paneIdx)
		}
		processSplitTree(targetPane, split, preWindow)
	}
}

func processSplitTree(targetID string, split Split, preWindow string) {
	if split.Type != "" {
		tmuxArg := "-" + normalizeSplitType(split.Type)

		cmdArgs := []string{"split-window", "-t", targetID, tmuxArg}
		if split.Size > 0 {
			cmdArgs = append(cmdArgs, "-p", fmt.Sprintf("%d", split.Size))
		}

		if out, err := tmux(cmdArgs...); err != nil {
			log.Printf("Warning: failed to split pane: %v\nOutput: %s", err, out)
			return
		}
	}

	sendKeys(targetID, preWindow)
	sendKeys(targetID, split.Command)

	// Process children in order; tmux assigns pane numbers sequentially after each split
	for i, child := range split.Children {
		childTarget := fmt.Sprintf("%s.%d", targetID, i)
		processSplitTree(childTarget, child, preWindow)
	}
}

func createPanesFromList(windowID string, panes []Pane, layout string, preWindow string) {
	if len(panes) == 0 {
		return
	}

	// Run setup + command in first pane
	sendKeys(windowID, preWindow)
	sendKeys(windowID, panes[0].Command)

	// Create additional panes and run their commands
	for i, pane := range panes[1:] {
		splitType := "-h"
		if i%2 == 1 {
			splitType = "-v"
		}

		if out, err := tmux("split-window", "-t", windowID, splitType); err != nil {
			log.Printf("Warning: failed to split pane: %v\nOutput: %s", err, out)
			continue
		}

		// Run setup + command in newly created (now active) pane
		sendKeys(windowID, preWindow)
		sendKeys(windowID, pane.Command)
	}

	// Apply layout
	if layout == "" && len(panes) > 1 {
		layout = "tiled"
	}
	if layout != "" {
		if _, err := tmux("select-layout", "-t", windowID, layout); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to apply layout '%s': %v\n", layout, err)
		}
	}
}

var startupWindowPattern = regexp.MustCompile(`^[a-zA-Z0-9_:\-.\s()[\]{}]+$`)

func selectStartupWindow(sessionName string, startupWindow string, startupPane *int) {
	// Reject control characters and other unexpected input in startup_window
	if !startupWindowPattern.MatchString(startupWindow) {
		fmt.Fprintf(os.Stderr, "Warning: invalid startup_window value: %s\n", startupWindow)
		return
	}

	// Try to select by window name first
	if _, err := tmux("select-window", "-t", sessionName+":"+startupWindow); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to select window '%s': %v\n", startupWindow, err)
		return
	}

	// Select pane within the window if specified (startupPane != nil)
	if startupPane != nil {
		paneTarget := fmt.Sprintf("%s:%s.%d", sessionName, startupWindow, *startupPane)
		if _, err := tmux("select-pane", "-t", paneTarget); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to select pane %d in window '%s': %v\n", *startupPane, startupWindow, err)
		}
	}
}

func killSession(s SessionConfig) {
	sessionName, absRoot := resolveSession(s)

	// Run on_project_exit hook before killing session
	if err := runShellHook(s.OnProjectExit, absRoot); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: on_project_exit failed: %v\n", err)
	}

	if out, err := tmux("kill-session", "-t", sessionName); err != nil {
		log.Fatalf("Failed to kill session: %v\nOutput: %s", err, out)
	}
	fmt.Printf("Killed session: %s\n", sessionName)
}
