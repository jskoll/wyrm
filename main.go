package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
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
	StartupPane    int    `toml:"startup_pane"`
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

func main() {
	configFile := flag.String("config", "", "Path to .tmuxconfig file")
	kill := flag.Bool("kill", false, "Kill the session instead of creating it")
	flag.Parse()

	if *configFile == "" {
		// Look for .tmuxconfig in current directory
		if _, err := os.Stat(".tmuxconfig"); err == nil {
			*configFile = ".tmuxconfig"
		} else {
			log.Fatal("No config file specified and .tmuxconfig not found in current directory")
		}
	}

	data, err := os.ReadFile(*configFile)
	if err != nil {
		log.Fatalf("Failed to read config: %v", err)
	}

	var config Config
	if err := toml.Unmarshal(data, &config); err != nil {
		log.Fatalf("Failed to parse config: %v", err)
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
	if s.OnProjectStart != "" {
		fmt.Println("Running on_project_start hook...")
		cmd := exec.Command("bash", "-c", s.OnProjectStart)
		cmd.Dir = absRoot
		if output, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: on_project_start failed: %v\nOutput: %s\n", err, output)
		}
	}

	// Kill existing session if it exists (wait a moment to ensure cleanup)
	exec.Command("tmux", "kill-session", "-t", sessionName).Run()
	time.Sleep(100 * time.Millisecond)

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
	attachCmd.Run()
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

		var cmdArgs []string
		cmdArgs = append(cmdArgs, "split-window", "-t", targetID, tmuxArg)
		if split.Size > 0 {
			cmdArgs = append(cmdArgs, "-p", fmt.Sprintf("%d", split.Size))
		}

		cmd := exec.Command("tmux", cmdArgs...)
		if err := cmd.Run(); err != nil {
			log.Printf("Warning: failed to split pane: %v", err)
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

	if len(panes) > 0 && panes[0].Command != "" && !strings.HasPrefix(panes[0].Command, "#") {
		cmd := exec.Command("tmux", "send-keys", "-t", windowID, panes[0].Command, "Enter")
		if output, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to run command '%s' in %s: %v\nOutput: %s\n", panes[0].Command, windowID, err, output)
		} else {
			fmt.Printf("  Running: %s\n", panes[0].Command)
		}
	}

	for i := 1; i < len(panes); i++ {
		pane := panes[i]

		var splitType string
		if i%2 == 1 {
			splitType = "-h"
		} else {
			splitType = "-v"
		}

		cmd := exec.Command("tmux", "split-window", "-t", windowID, splitType)
		if err := cmd.Run(); err != nil {
			log.Printf("Warning: failed to split pane: %v", err)
			continue
		}

		if pane.Command != "" {
			cmd := exec.Command("tmux", "send-keys", "-t", windowID, pane.Command, "Enter")
			cmd.Run()
		}
	}

	if layout != "" {
		cmd := exec.Command("tmux", "select-layout", "-t", windowID, layout)
		cmd.Run()
	} else if len(panes) > 1 {
		cmd := exec.Command("tmux", "select-layout", "-t", windowID, "tiled")
		cmd.Run()
	}
}

func selectStartupWindow(sessionName string, startupWindow string, startupPane int) {
	// Try to select by window name first
	selectCmd := exec.Command("tmux", "select-window", "-t", sessionName+":"+startupWindow)
	if err := selectCmd.Run(); err != nil {
		// If that fails, try as a number
		selectCmd = exec.Command("tmux", "select-window", "-t", sessionName+":"+startupWindow)
		selectCmd.Run()
	}

	// Select pane within the window if specified
	if startupPane > 0 {
		paneCmd := exec.Command("tmux", "select-pane", "-t", sessionName+":"+startupWindow+"."+fmt.Sprintf("%d", startupPane))
		paneCmd.Run()
	}
}

func killSession(sessionName string, config Config) {
	// Run on_project_exit hook before killing session
	if config.Session.OnProjectExit != "" {
		fmt.Println("Running on_project_exit hook...")
		root := os.ExpandEnv(config.Session.Root)
		absRoot, _ := filepath.Abs(root)
		cmd := exec.Command("bash", "-c", config.Session.OnProjectExit)
		cmd.Dir = absRoot
		if output, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: on_project_exit failed: %v\nOutput: %s\n", err, output)
		}
	}

	cmd := exec.Command("tmux", "kill-session", "-t", sessionName)
	if err := cmd.Run(); err != nil {
		log.Fatalf("Failed to kill session: %v", err)
	}
	fmt.Printf("Killed session: %s\n", sessionName)
}
