package tui

import (
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jskoll/wyrm/internal/config"
	"github.com/jskoll/wyrm/internal/editor"
	"github.com/jskoll/wyrm/internal/session"
	"github.com/jskoll/wyrm/internal/sessions"
	"github.com/jskoll/wyrm/internal/state"
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
	// Root and Wildcard mirror config.Project — see DiscoverWildcardProjects.
	// Root is the matched directory for a wildcard-discovered project
	// (nonempty only when Wildcard is true), which overrides the template
	// config's own session.root when the project is started.
	Root     string
	Wildcard bool
}

// listProjects annotates config.DiscoverProjects — the shared discovery rules,
// used identically by `wyrm <name>` and `wyrm list-configs` — with whether a
// session by each project's name is currently running.
func listProjects(r tmux.Runner, settings *config.Settings) ([]Project, error) {
	running := map[string]string{}
	if sessions, err := sessions.List(r); err == nil {
		for _, s := range sessions {
			running[s.Name] = s.ID
		}
	}

	discovered := config.DiscoverProjects(settings)
	projects := make([]Project, 0, len(discovered))
	for _, d := range discovered {
		p := Project{Name: d.Name, Path: d.Path, Shared: d.Shared, Root: d.Root, Wildcard: d.Wildcard}
		if id, ok := running[d.Name]; ok {
			p.Running, p.SessionID = true, id
		}
		projects = append(projects, p)
	}
	return projects, nil
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
// project and resolves to a projectStartedMsg carrying the session ID to
// attach to. session.Create is idempotent, so this doubles as "attach".
func startProjectCmd(r tmux.Runner, p Project) tea.Cmd {
	return func() tea.Msg {
		cfg, err := config.Load(p.Path)
		if err != nil {
			return projectStartedMsg{err: err}
		}
		if p.Wildcard {
			// The template's own session.root (normally unset) doesn't say
			// which directory this Project stands for — only the match
			// does. See config.DiscoverWildcardProjects.
			cfg.Session.Root = p.Root
		}
		hist, err := state.Load()
		if err != nil {
			return projectStartedMsg{err: err}
		}
		_, id, _, err := session.Create(r, cfg, io.Discard, io.Discard, session.WithHistory(hist))
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
// resolution is shared with `wyrm edit`.
func editConfigCmd(r tmux.Runner, settings *config.Settings, path string) tea.Cmd {
	c, err := editor.Command(path)
	if err != nil {
		return func() tea.Msg { return actionErrMsg{err} }
	}
	return tea.ExecProcess(c, func(err error) tea.Msg {
		if err != nil {
			return actionErrMsg{err}
		}
		ps, lerr := listProjects(r, settings)
		return projectsMsg{projects: ps, err: lerr}
	})
}
