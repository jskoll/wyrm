package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
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
	Panes  []Pane `toml:"panes"`
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
	cmd := exec.Command("tmux", "new-session", "-d", "-s", sessionName, "-c", absRoot, "-n", firstWindow.Name)
	if output, err := cmd.CombinedOutput(); err != nil {
		log.Fatalf("Failed to create session: %v\nOutput: %s", err, output)
	}

	// Create panes in first window
	createPanes(sessionName, 0, firstWindow)

	// Create additional windows
	for i := 1; i < len(windows); i++ {
		window := windows[i]
		cmd := exec.Command("tmux", "new-window", "-t", sessionName, "-n", window.Name, "-c", absRoot)
		if output, err := cmd.CombinedOutput(); err != nil {
			log.Fatalf("Failed to create window '%s': %v\nOutput: %s", window.Name, err, output)
		}
		createPanes(sessionName, i, window)
	}

	// Attach to session
	fmt.Printf("Created session: %s\n", sessionName)
	attachCmd := exec.Command("tmux", "attach-session", "-t", sessionName)
	attachCmd.Stdin = os.Stdin
	attachCmd.Stdout = os.Stdout
	attachCmd.Stderr = os.Stderr
	attachCmd.Run()
}

func createPanes(sessionName string, windowIndex int, window Window) {
	if len(window.Panes) == 0 {
		return
	}

	windowID := fmt.Sprintf("%s:%d", sessionName, windowIndex)

	// First pane already exists, run its command
	if len(window.Panes) > 0 && window.Panes[0].Command != "" {
		cmd := exec.Command("tmux", "send-keys", "-t", windowID, window.Panes[0].Command, "Enter")
		cmd.Run()
	}

	// Create additional panes with splits
	for i := 1; i < len(window.Panes); i++ {
		pane := window.Panes[i]

		// Split window (alternate between vertical/horizontal)
		var splitType string
		if i%2 == 1 {
			splitType = "-h" // horizontal split
		} else {
			splitType = "-v" // vertical split
		}

		cmd := exec.Command("tmux", "split-window", "-t", windowID, splitType)
		if err := cmd.Run(); err != nil {
			log.Printf("Warning: failed to split pane: %v", err)
			continue
		}

		// Run command in new pane
		if pane.Command != "" {
			cmd := exec.Command("tmux", "send-keys", "-t", windowID, pane.Command, "Enter")
			cmd.Run()
		}
	}

	// Apply layout if specified
	if window.Layout != "" {
		cmd := exec.Command("tmux", "select-layout", "-t", windowID, window.Layout)
		cmd.Run()
	} else if len(window.Panes) > 1 {
		// Default to tiled layout for multiple panes
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
