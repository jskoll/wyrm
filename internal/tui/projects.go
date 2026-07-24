package tui

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jskoll/wyrm/internal/config"
	"github.com/jskoll/wyrm/internal/picker"
	"github.com/jskoll/wyrm/internal/session"
	"github.com/jskoll/wyrm/internal/tmux"
)

// Project is a discoverable wyrm config the TUI can start, attach to, edit, or
// stop. Name is the session name the config would produce; Running/SessionID
// are filled by joining against the live session list.
type Project struct {
	Name      string
	Path      string
	Shared    bool
	Running   bool
	SessionID string
}

// listProjects enumerates the configs wyrm can see — the local .wyrm.toml/
// .tmuxconfig in the cwd, plus every "<folder>.wyrm.toml" in the shared config
// directory — mirroring the discovery behind -list-configs. Each is annotated
// with whether a session by its name is currently running.
func listProjects(r tmux.Runner, settings *config.Settings) ([]Project, error) {
	running := map[string]string{}
	if sessions, err := picker.ListSessions(r); err == nil {
		for _, s := range sessions {
			running[s.Name] = s.ID
		}
	}

	var projects []Project
	seen := map[string]bool{}
	add := func(path string, shared bool) {
		abs, err := filepath.Abs(path)
		if err != nil {
			abs = path
		}
		if seen[abs] {
			return
		}
		seen[abs] = true
		name := projectName(path, shared)
		if name == "" {
			return
		}
		p := Project{Name: name, Path: path, Shared: shared}
		if id, ok := running[name]; ok {
			p.Running, p.SessionID = true, id
		}
		projects = append(projects, p)
	}

	for _, name := range []string{config.DefaultFileName, config.LegacyFileName} {
		if _, err := os.Stat(name); err == nil {
			add(name, false)
		}
	}
	if settings != nil {
		if dir, err := settings.ResolvedSharedDir(); err == nil {
			matches, _ := filepath.Glob(filepath.Join(dir, "*"+config.DefaultFileName))
			sort.Strings(matches)
			for _, m := range matches {
				add(m, true)
			}
		}
	}
	return projects, nil
}

// projectName is the session name a config produces: its explicit session.name,
// else (for a local config) the resolved root basename, else the filename with
// the .wyrm.toml suffix stripped.
func projectName(path string, shared bool) string {
	if cfg, err := config.Load(path); err == nil {
		if cfg.Session.Name != "" {
			return cfg.Session.Name
		}
		if !shared {
			if name, _, err := cfg.Session.Resolve(); err == nil {
				return name
			}
		}
	}
	base := filepath.Base(path)
	if shared {
		return strings.TrimSuffix(base, config.DefaultFileName)
	}
	if cwd, err := os.Getwd(); err == nil {
		return filepath.Base(cwd)
	}
	return base
}

// --- messages ---

type projectsMsg struct {
	projects []Project
	err      error
}

type configPreviewMsg struct {
	path    string
	content string
	err     error
}

type projectStartedMsg struct {
	sessionID string
	err       error
}

// --- commands ---

func loadProjects(r tmux.Runner, settings *config.Settings) tea.Cmd {
	return func() tea.Msg {
		ps, err := listProjects(r, settings)
		return projectsMsg{projects: ps, err: err}
	}
}

func loadConfigPreview(path string) tea.Cmd {
	return func() tea.Msg {
		data, err := os.ReadFile(path)
		return configPreviewMsg{path: path, content: string(data), err: err}
	}
}

// startProjectCmd builds (or, if already running, reuses) the session for a
// config and resolves to a projectStartedMsg carrying the session ID to attach
// to. session.Create is idempotent, so this doubles as "attach".
func startProjectCmd(r tmux.Runner, path string) tea.Cmd {
	return func() tea.Msg {
		cfg, err := config.Load(path)
		if err != nil {
			return projectStartedMsg{err: err}
		}
		_, id, _, err := session.Create(r, cfg, io.Discard, io.Discard)
		return projectStartedMsg{sessionID: id, err: err}
	}
}

// killProjectCmd stops a project's session, running its on_project_exit hook
// (unlike the hook-less session kills), then re-lists projects to refresh the
// running annotation.
func killProjectCmd(r tmux.Runner, settings *config.Settings, path string) tea.Cmd {
	return func() tea.Msg {
		cfg, err := config.Load(path)
		if err != nil {
			return actionErrMsg{err}
		}
		if _, err := session.Kill(r, cfg, io.Discard); err != nil {
			return actionErrMsg{err}
		}
		ps, lerr := listProjects(r, settings)
		return projectsMsg{projects: ps, err: lerr}
	}
}

// editConfigCmd opens the config in $EDITOR (suspending the TUI via
// tea.ExecProcess and resuming after), then re-lists projects. Editor
// resolution mirrors `wyrm -edit`.
func editConfigCmd(r tmux.Runner, settings *config.Settings, path string) tea.Cmd {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	parts := strings.Fields(editor)
	if len(parts) == 0 {
		return func() tea.Msg { return actionErrMsg{errors.New("$EDITOR is set but empty")} }
	}
	c := exec.Command(parts[0], append(parts[1:], path)...)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		if err != nil {
			return actionErrMsg{err}
		}
		ps, lerr := listProjects(r, settings)
		return projectsMsg{projects: ps, err: lerr}
	})
}
