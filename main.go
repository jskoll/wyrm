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
	Name string `toml:"name"`
	Root string `toml:"root"`
}

type Window struct {
	Name   string `toml:"name"`
	Layout string `toml:"layout"`
	Splits []Split `toml:"splits"`
	Panes  []Pane `toml:"panes"`
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
		killSession(config.Session.Name)
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

	if len(window.Splits) > 0 {
		createPanesFromSplits(sessionName, windowIndex, window.Splits)
	} else if len(window.Panes) > 0 {
		createPanesFromList(sessionName, windowIndex, window.Panes, window.Layout)
	}
}

func createPanesFromSplits(sessionName string, windowIndex int, splits []Split) {
	windowID := fmt.Sprintf("%s:%d", sessionName, windowIndex)
	paneCounter := 0
	processSplits(windowID, splits, &paneCounter)
}

func processSplits(windowID string, splits []Split, paneCounter *int) {
	for i, split := range splits {
		if i > 0 && split.Type != "" {
			splitType := normalizeSplitType(split.Type)
			tmuxArg := "-v"
			if splitType == "h" {
				tmuxArg = "-h"
			}

			var cmdArgs []string
			cmdArgs = append(cmdArgs, "split-window", "-t", windowID, tmuxArg)
			if split.Size > 0 {
				cmdArgs = append(cmdArgs, "-p", fmt.Sprintf("%d", split.Size))
			}

			cmd := exec.Command("tmux", cmdArgs...)
			if err := cmd.Run(); err != nil {
				log.Printf("Warning: failed to split pane: %v", err)
				continue
			}
		}

		if split.Command != "" && !strings.HasPrefix(split.Command, "#") {
			paneID := windowID
			if *paneCounter > 0 {
				paneID = fmt.Sprintf("%s.%d", windowID, *paneCounter)
			}

			execCmd := exec.Command("tmux", "send-keys", "-t", paneID, split.Command, "Enter")
			if output, err := execCmd.CombinedOutput(); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to run command '%s' in %s: %v\nOutput: %s\n", split.Command, paneID, err, output)
			} else {
				fmt.Printf("  Running: %s\n", split.Command)
			}
		}

		*paneCounter++

		if len(split.Children) > 0 {
			processSplits(windowID, split.Children, paneCounter)
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

func killSession(name string) {
	cmd := exec.Command("tmux", "kill-session", "-t", name)
	if err := cmd.Run(); err != nil {
		log.Fatalf("Failed to kill session: %v", err)
	}
	fmt.Printf("Killed session: %s\n", name)
}
