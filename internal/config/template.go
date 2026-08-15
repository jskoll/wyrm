package config

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Layout preset constants for interactive scaffolding.
const (
	PresetSingle                  = 1
	PresetTwoPaneVertical         = 2
	PresetTwoPaneHorizontal       = 3
	PresetThreePaneEditorStack    = 4
	PresetThreePaneMainHorizontal = 5
)

// WindowSpec describes a window to generate in a custom config.
type WindowSpec struct {
	Name     string
	Preset   int
	Commands []string
}

// TemplateDef describes a starter template and its aliases.
type TemplateDef struct {
	Name        string
	Aliases     []string
	Description string
	Template    string
}

var starterTemplates = []TemplateDef{
	{
		Name:        "node",
		Aliases:     []string{"nodejs", "javascript", "js", "ts", "typescript"},
		Description: "Node.js project (dev server + test watch)",
		Template: `# Node.js project configuration
# Run 'wyrm' to start this session.

[session]
name = %s
root = %s

[[windows]]
name = "dev"

  [[windows.splits]]
  command = "$EDITOR ."

  [[windows.splits]]
  type = "h"          # split right: watcher gets 35%% width
  size = 35
  command = "npm run dev"

[[windows]]
name = "test"

  [[windows.splits]]
  command = "npm test -- --watch"

  [[windows.splits]]
  type = "v"          # split below: 50%% height
  size = 50
  command = "# test logs / terminal"
`,
	},
	{
		Name:        "python",
		Aliases:     []string{"py"},
		Description: "Python project (editor + pytest + repl)",
		Template: `# Python project configuration
# Run 'wyrm' to start this session.

[session]
name = %s
root = %s

[[windows]]
name = "editor"

  [[windows.splits]]
  command = "$EDITOR ."

  [[windows.splits]]
  type = "h"          # split right: 30%% width
  size = 30
  command = "# venv: source .venv/bin/activate"

[[windows]]
name = "test"

  [[windows.splits]]
  command = "pytest -v --tb=short"

  [[windows.splits]]
  type = "v"          # split below: 50%% height
  size = 50
  command = "# coverage / lint"

[[windows]]
name = "repl"

  [[windows.splits]]
  command = "python"
`,
	},
	{
		Name:        "go",
		Aliases:     []string{"golang"},
		Description: "Go project (editor + test watch + server)",
		Template: `# Go project configuration
# Run 'wyrm' to start this session.

[session]
name = %s
root = %s

[[windows]]
name = "code"

  [[windows.splits]]
  command = "$EDITOR ."

  [[windows.splits]]
  type = "h"          # split right: 35%% width
  size = 35
  command = "go test -v ./..."

    [[windows.splits.children]]
    type = "v"        # split below: 50%% height
    size = 50
    command = "# go run ."

[[windows]]
name = "server"

  [[windows.splits]]
  command = "# go run main.go"
`,
	},
	{
		Name:        "rust",
		Aliases:     []string{"rs"},
		Description: "Rust project (editor + test + check + run)",
		Template: `# Rust project configuration
# Run 'wyrm' to start this session.

[session]
name = %s
root = %s

[[windows]]
name = "editor"

  [[windows.splits]]
  command = "$EDITOR ."

  [[windows.splits]]
  type = "h"          # split right: 35%% width
  size = 35
  command = "cargo test"

    [[windows.splits.children]]
    type = "v"        # split below: 50%% height
    size = 50
    command = "cargo check"

[[windows]]
name = "run"

  [[windows.splits]]
  command = "cargo run"
`,
	},
	{
		Name:        "monorepo",
		Aliases:     []string{"mono", "workspace", "workspaces"},
		Description: "Monorepo layout (services + packages + git)",
		Template: `# Monorepo configuration
# Run 'wyrm' to start this session.

[session]
name = %s
root = %s

  # Shared environment variables for all panes
  # [session.env]
  # NODE_ENV = "development"

[[windows]]
name = "services"
root = "."

  [[windows.splits]]
  command = "$EDITOR ."

  [[windows.splits]]
  type = "h"          # split right: 40%% width
  size = 40
  command = "# npm run dev / docker compose up"

[[windows]]
name = "packages"
root = "."

  [[windows.splits]]
  command = "# build or watch shared packages"

[[windows]]
name = "git"

  [[windows.splits]]
  command = "lazygit"
`,
	},
	{
		Name:        "minimal",
		Aliases:     []string{"default", "basic", "simple"},
		Description: "Minimal 2-pane layout (editor + shell)",
		Template: `# Minimal project configuration
# Run 'wyrm' to start this session.

[session]
name = %s
root = %s

[[windows]]
name = "main"

  [[windows.splits]]
  command = "$EDITOR ."

  [[windows.splits]]
  type = "h"          # split horizontally: new pane to the right
  size = 30           # new pane gets 30%% of the width
`,
	},
}

// AvailableTemplates returns the canonical list of available template names.
func AvailableTemplates() []string {
	names := make([]string, len(starterTemplates))
	for i, t := range starterTemplates {
		names[i] = t.Name
	}
	sort.Strings(names)
	return names
}

// FindTemplate looks up a starter template by name or alias.
func FindTemplate(name string) (TemplateDef, bool) {
	norm := strings.ToLower(strings.TrimSpace(name))
	for _, t := range starterTemplates {
		if strings.EqualFold(t.Name, norm) {
			return t, true
		}
		for _, alias := range t.Aliases {
			if strings.EqualFold(alias, norm) {
				return t, true
			}
		}
	}
	return TemplateDef{}, false
}

// GetTemplate formats a starter template with the given session name and root.
func GetTemplate(templateName, sessionName, sessionRoot string) (string, error) {
	tmpl, ok := FindTemplate(templateName)
	if !ok {
		return "", fmt.Errorf("unknown template %q (available: %s)", templateName, strings.Join(AvailableTemplates(), ", "))
	}
	if sessionName == "" {
		sessionName = "myproject"
	}
	if sessionRoot == "" {
		sessionRoot = "."
	}
	return fmt.Sprintf(tmpl.Template, strconv.Quote(sessionName), strconv.Quote(sessionRoot)), nil
}

// GenerateCustomConfig formats a custom wyrm configuration from window specifications.
func GenerateCustomConfig(sessionName, sessionRoot string, windows []WindowSpec) string {
	if sessionName == "" {
		sessionName = "myproject"
	}
	if sessionRoot == "" {
		sessionRoot = "."
	}
	if len(windows) == 0 {
		windows = []WindowSpec{
			{
				Name:     "main",
				Preset:   PresetTwoPaneVertical,
				Commands: []string{"$EDITOR .", ""},
			},
		}
	}

	var sb strings.Builder
	sb.WriteString("# Wyrm session configuration\n")
	sb.WriteString("# Run 'wyrm' to start this session.\n\n")
	sb.WriteString("[session]\n")
	_, _ = fmt.Fprintf(&sb, "name = %s\n", strconv.Quote(sessionName))
	_, _ = fmt.Fprintf(&sb, "root = %s\n\n", strconv.Quote(sessionRoot))
	sb.WriteString("# Lifecycle hooks (optional):\n")
	sb.WriteString("# on_project_start = \"echo 'Starting up...'\"\n")
	sb.WriteString("# on_project_exit = \"echo 'Cleaning up...'\"\n\n")

	for i, win := range windows {
		winName := win.Name
		if winName == "" {
			if i == 0 {
				winName = "main"
			} else {
				winName = fmt.Sprintf("window%d", i+1)
			}
		}

		sb.WriteString("[[windows]]\n")
		_, _ = fmt.Fprintf(&sb, "name = %s\n\n", strconv.Quote(winName))

		cmdAt := func(idx int) string {
			if idx < len(win.Commands) {
				return win.Commands[idx]
			}
			return ""
		}

		switch win.Preset {
		case PresetSingle:
			c1 := cmdAt(0)
			sb.WriteString("  [[windows.splits]]\n")
			if c1 != "" {
				_, _ = fmt.Fprintf(&sb, "  command = %s\n\n", strconv.Quote(c1))
			} else {
				sb.WriteString("  # command = \"\"\n\n")
			}

		case PresetTwoPaneVertical:
			c1, c2 := cmdAt(0), cmdAt(1)
			sb.WriteString("  [[windows.splits]]\n")
			if c1 != "" {
				_, _ = fmt.Fprintf(&sb, "  command = %s\n\n", strconv.Quote(c1))
			} else {
				sb.WriteString("  # command = \"\"\n\n")
			}
			sb.WriteString("  [[windows.splits]]\n")
			sb.WriteString("  type = \"h\"          # split right: 50% width\n")
			sb.WriteString("  size = 50\n")
			if c2 != "" {
				_, _ = fmt.Fprintf(&sb, "  command = %s\n\n", strconv.Quote(c2))
			} else {
				sb.WriteString("\n")
			}

		case PresetTwoPaneHorizontal:
			c1, c2 := cmdAt(0), cmdAt(1)
			sb.WriteString("  [[windows.splits]]\n")
			if c1 != "" {
				_, _ = fmt.Fprintf(&sb, "  command = %s\n\n", strconv.Quote(c1))
			} else {
				sb.WriteString("  # command = \"\"\n\n")
			}
			sb.WriteString("  [[windows.splits]]\n")
			sb.WriteString("  type = \"v\"          # split below: 50% height\n")
			sb.WriteString("  size = 50\n")
			if c2 != "" {
				_, _ = fmt.Fprintf(&sb, "  command = %s\n\n", strconv.Quote(c2))
			} else {
				sb.WriteString("\n")
			}

		case PresetThreePaneEditorStack:
			c1, c2, c3 := cmdAt(0), cmdAt(1), cmdAt(2)
			sb.WriteString("  [[windows.splits]]\n")
			if c1 != "" {
				_, _ = fmt.Fprintf(&sb, "  command = %s\n\n", strconv.Quote(c1))
			} else {
				sb.WriteString("  # command = \"\"\n\n")
			}
			sb.WriteString("  [[windows.splits]]\n")
			sb.WriteString("  type = \"h\"          # split right: 35% width\n")
			sb.WriteString("  size = 35\n")
			if c2 != "" {
				_, _ = fmt.Fprintf(&sb, "  command = %s\n\n", strconv.Quote(c2))
			} else {
				sb.WriteString("\n")
			}
			sb.WriteString("    [[windows.splits.children]]\n")
			sb.WriteString("    type = \"v\"        # split below: 50% height\n")
			sb.WriteString("    size = 50\n")
			if c3 != "" {
				_, _ = fmt.Fprintf(&sb, "    command = %s\n\n", strconv.Quote(c3))
			} else {
				sb.WriteString("\n")
			}

		case PresetThreePaneMainHorizontal:
			c1, c2, c3 := cmdAt(0), cmdAt(1), cmdAt(2)
			sb.WriteString("  [[windows.splits]]\n")
			if c1 != "" {
				_, _ = fmt.Fprintf(&sb, "  command = %s\n\n", strconv.Quote(c1))
			} else {
				sb.WriteString("  # command = \"\"\n\n")
			}
			sb.WriteString("  [[windows.splits]]\n")
			sb.WriteString("  type = \"v\"          # split below: 40% height\n")
			sb.WriteString("  size = 40\n")
			if c2 != "" {
				_, _ = fmt.Fprintf(&sb, "  command = %s\n\n", strconv.Quote(c2))
			} else {
				sb.WriteString("\n")
			}
			sb.WriteString("    [[windows.splits.children]]\n")
			sb.WriteString("    type = \"h\"        # split right: 50% width\n")
			sb.WriteString("    size = 50\n")
			if c3 != "" {
				_, _ = fmt.Fprintf(&sb, "    command = %s\n\n", strconv.Quote(c3))
			} else {
				sb.WriteString("\n")
			}

		default:
			// Fallback to single pane
			c1 := cmdAt(0)
			sb.WriteString("  [[windows.splits]]\n")
			if c1 != "" {
				_, _ = fmt.Fprintf(&sb, "  command = %s\n\n", strconv.Quote(c1))
			} else {
				sb.WriteString("  # command = \"\"\n\n")
			}
		}
	}

	return strings.TrimRight(sb.String(), "\n") + "\n"
}
