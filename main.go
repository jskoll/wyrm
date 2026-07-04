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
	Session SessionConfig   `toml:"session"`
	Windows []Window        `toml:"windows"`
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
	Name      string `toml:"name"`
	Layout    string `toml:"layout"`
	Splits    []Split `toml:"splits"`
	Panes     []Pane `toml:"panes"`
	PreWindow string `toml:"pre_window"`
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

func isPathInDirectory(path, dir string) bool {
	// Ensure directory has trailing separator to prevent /tmp-evil matching /tmp
	if !strings.HasSuffix(dir, "/") {
		dir = dir + "/"
	}
	// Check both with and without trailing separator for root directories
	return strings.HasPrefix(path, dir) || path == strings.TrimSuffix(dir, "/")
}

func validateConfigPath(path string) error {
	// Prevent path traversal by checking before and after cleaning
	if strings.Contains(path, "..") {
		return fmt.Errorf("path traversal detected: %s", path)
	}
	// Also check if absolute path goes outside expected boundaries
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("invalid path: %s", path)
	}
	// Ensure the absolute path is under common safe directories
	isInSafeDir := false

	// Check home directory (with boundary verification)
	if homeDir, err := os.UserHomeDir(); err == nil && homeDir != "" && isPathInDirectory(absPath, homeDir) {
		isInSafeDir = true
	}
	// Check common temp directories (with boundary verification)
	if isPathInDirectory(absPath, "/tmp") || isPathInDirectory(absPath, "/var/tmp") {
		isInSafeDir = true
	}
	// Check current working directory (with boundary verification)
	if cwd, err := os.Getwd(); err == nil && cwd != "" && isPathInDirectory(absPath, cwd) {
		isInSafeDir = true
	}

	if !isInSafeDir {
		return fmt.Errorf("config path must be in home directory, /tmp, /var/tmp, or current working directory: %s", path)
	}
	return nil
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
	configFile := flag.String("config", "", "Path to .tmuxconfig file")
	kill := flag.Bool("kill", false, "Kill the session instead of creating it")
	flag.Parse()

	if *configFile == "" {
		log.Fatal("Error: -config flag is required (implicit .tmuxconfig discovery disabled for security)")
	}

	if err := validateConfigPath(*configFile); err != nil {
		log.Fatalf("Invalid config path: %v", err)
	}

	data, err := os.ReadFile(*configFile)
	if err != nil {
		log.Fatalf("Failed to read config: %v", err)
	}

	var config Config
	if err := toml.Unmarshal(data, &config); err != nil {
		log.Fatalf("Failed to parse config: %v", err)
	}

	// Validate critical config fields
	if config.Session.Name == "" && config.Session.Root == "" {
		log.Fatal("Config must specify either session.name or session.root")
	}
	if err := validateConfigPath(config.Session.Root); err != nil {
		log.Fatalf("Invalid root path in config: %v", err)
	}

	if *kill {
		killSession(config.Session.Name, config)
		return
	}

	createSession(config.Session, config.Windows)
}

func createSession(s SessionConfig, windows []Window) {
	// Expand root path
	root := os.ExpandEnv(s.Root)
	absRoot, err := filepath.Abs(root)
	if err != nil {
		log.Fatalf("Invalid root path: %v", err)
	}

	sessionName := s.Name
	if sessionName == "" {
		sessionName = filepath.Base(absRoot)
	}

	// Run on_project_start hook before creating session
	if err := runShellHook(s.OnProjectStart, absRoot); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: on_project_start failed: %v\n", err)
	}

	// Kill existing session if it exists (wait a moment to ensure cleanup)
	exec.Command("tmux", "kill-session", "-t", sessionName).Run()
	time.Sleep(50 * time.Millisecond)

	// Create new session with first window
	if len(windows) == 0 {
		log.Fatal("No windows defined in config")
	}

	firstWindow := windows[0]
	// Create session WITH first window to ensure window 0 exists
	cmd := exec.Command("tmux", "new-session", "-d", "-s", sessionName, "-n", firstWindow.Name, "-c", absRoot)
	if output, err := cmd.CombinedOutput(); err != nil {
		log.Fatalf("Failed to create session: %v\nOutput: %s", err, output)
	}

	// Create panes in first window (window 1 in tmux numbering)
	fmt.Printf("Creating window 1: %s\n", firstWindow.Name)
	createPanes(sessionName, 1, firstWindow)

	// Create additional windows
	for i := 1; i < len(windows); i++ {
		window := windows[i]
		cmd := exec.Command("tmux", "new-window", "-t", sessionName, "-n", window.Name, "-c", absRoot)
		if output, err := cmd.CombinedOutput(); err != nil {
			log.Fatalf("Failed to create window '%s': %v\nOutput: %s", window.Name, err, output)
		}
		tmuxIndex := i + 1 // tmux windows are numbered 1+
		fmt.Printf("Creating window %d: %s\n", tmuxIndex, window.Name)
		createPanes(sessionName, tmuxIndex, window)
	}

	// Select startup window if specified
	if s.StartupWindow != "" {
		selectStartupWindow(sessionName, s.StartupWindow, s.StartupPane)
	}

	// Attach to session
	fmt.Printf("Created session: %s\n", sessionName)
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
	case "v", "vertical":
		return "v"
	default:
		return "v"
	}
}

func createPanes(sessionName string, windowIndex int, window Window) {
	// Allow tmux time to initialize window before sending commands
	time.Sleep(50 * time.Millisecond)

	// Run pre_window command if specified
	if window.PreWindow != "" {
		windowID := fmt.Sprintf("%s:%d", sessionName, windowIndex)
		cmd := exec.Command("tmux", "send-keys", "-t", windowID, window.PreWindow, "Enter")
		if output, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: pre_window command failed: %v\nOutput: %s\n", err, output)
		} else {
			fmt.Printf("  Pre-window: %s\n", window.PreWindow)
		}
		// Allow time for pre_window command to complete before creating panes
		time.Sleep(50 * time.Millisecond)
	}

	if len(window.Splits) > 0 {
		createPanesFromSplits(sessionName, windowIndex, window.Splits)
	} else if len(window.Panes) > 0 {
		createPanesFromList(sessionName, windowIndex, window.Panes, window.Layout)
	}
}

func createPanesFromSplits(sessionName string, windowIndex int, splits []Split) {
	windowID := fmt.Sprintf("%s:%d", sessionName, windowIndex)
	paneIdx := 0

	for _, split := range splits {
		targetPane := windowID
		if paneIdx > 0 {
			targetPane = fmt.Sprintf("%s.%d", windowID, paneIdx)
		}

		processSplitTree(targetPane, split)
		paneIdx++
	}
}

func processSplitTree(targetID string, split Split) {
	if split.Type != "" {
		splitType := normalizeSplitType(split.Type)
		tmuxArg := "-v"
		if splitType == "h" {
			tmuxArg = "-h"
		}

		cmdArgs := make([]string, 0, 6)
		cmdArgs = append(cmdArgs, "split-window", "-t", targetID, tmuxArg)
		if split.Size > 0 {
			cmdArgs = append(cmdArgs, "-p", fmt.Sprintf("%d", split.Size))
		}

		cmd := exec.Command("tmux", cmdArgs...)
		if output, err := cmd.CombinedOutput(); err != nil {
			log.Printf("Warning: failed to split pane: %v\nOutput: %s", err, output)
			return
		}
	}

	if split.Command != "" && !strings.HasPrefix(split.Command, "#") {
		execCmd := exec.Command("tmux", "send-keys", "-t", targetID, split.Command, "Enter")
		if output, err := execCmd.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to run command '%s' in %s: %v\nOutput: %s\n", split.Command, targetID, err, output)
		} else {
			fmt.Printf("  Running: %s\n", split.Command)
		}
	}

	// Process children in order; tmux assigns pane numbers sequentially after each split
	if len(split.Children) > 0 {
		for i, child := range split.Children {
			childTarget := fmt.Sprintf("%s.%d", targetID, i)
			processSplitTree(childTarget, child)
		}
	}
}

func createPanesFromList(sessionName string, windowIndex int, panes []Pane, layout string) {
	if len(panes) == 0 {
		return
	}

	windowID := fmt.Sprintf("%s:%d", sessionName, windowIndex)

	// Run command in first pane
	if panes[0].Command != "" && !strings.HasPrefix(panes[0].Command, "#") {
		cmd := exec.Command("tmux", "send-keys", "-t", windowID, panes[0].Command, "Enter")
		if output, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to run command '%s' in pane 0: %v\nOutput: %s\n", panes[0].Command, err, output)
		} else {
			fmt.Printf("  Running: %s\n", panes[0].Command)
		}
	}

	// Create additional panes and run their commands
	for i := 1; i < len(panes); i++ {
		pane := panes[i]

		var splitType string
		if i%2 == 1 {
			splitType = "-h"
		} else {
			splitType = "-v"
		}

		cmd := exec.Command("tmux", "split-window", "-t", windowID, splitType)
		if output, err := cmd.CombinedOutput(); err != nil {
			log.Printf("Warning: failed to split pane: %v\nOutput: %s", err, output)
			continue
		}

		// Run command in newly created pane
		if pane.Command != "" && !strings.HasPrefix(pane.Command, "#") {
			cmd := exec.Command("tmux", "send-keys", "-t", windowID, pane.Command, "Enter")
			if output, err := cmd.CombinedOutput(); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to run command '%s' in pane %d: %v\nOutput: %s\n", pane.Command, i, err, output)
			} else {
				fmt.Printf("  Running: %s\n", pane.Command)
			}
		}
	}

	// Apply layout
	if layout != "" {
		cmd := exec.Command("tmux", "select-layout", "-t", windowID, layout)
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to apply layout '%s': %v\n", layout, err)
		}
	} else if len(panes) > 1 {
		cmd := exec.Command("tmux", "select-layout", "-t", windowID, "tiled")
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to apply tiled layout: %v\n", err)
		}
	}
}

func selectStartupWindow(sessionName string, startupWindow string, startupPane *int) {
	// Validate startup_window value - allow most printable chars except control chars
	// Accept alphanumerics, spaces, underscores, hyphens, colons, dots, parens, brackets
	if !regexp.MustCompile(`^[a-zA-Z0-9_:\-.\s()[\]{}]+$`).MatchString(startupWindow) {
		fmt.Fprintf(os.Stderr, "Warning: invalid startup_window value: %s\n", startupWindow)
		return
	}

	// Try to select by window name first
	selectCmd := exec.Command("tmux", "select-window", "-t", sessionName+":"+startupWindow)
	if err := selectCmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to select window '%s': %v\n", startupWindow, err)
		return
	}

	// Select pane within the window if specified (startupPane != nil)
	if startupPane != nil {
		paneTarget := fmt.Sprintf("%s:%s.%d", sessionName, startupWindow, *startupPane)
		paneCmd := exec.Command("tmux", "select-pane", "-t", paneTarget)
		if err := paneCmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to select pane %d in window '%s': %v\n", *startupPane, startupWindow, err)
		}
	}
}

func killSession(sessionName string, config Config) {
	// Run on_project_exit hook before killing session
	root := os.ExpandEnv(config.Session.Root)
	absRoot, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to resolve root path: %v\n", err)
		absRoot = root // fall back to unexpanded path
	}

	if err := runShellHook(config.Session.OnProjectExit, absRoot); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: on_project_exit failed: %v\n", err)
	}

	cmd := exec.Command("tmux", "kill-session", "-t", sessionName)
	if err := cmd.Run(); err != nil {
		log.Fatalf("Failed to kill session: %v", err)
	}
	fmt.Printf("Killed session: %s\n", sessionName)
}
