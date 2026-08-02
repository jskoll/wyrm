package tui

import (
	"io"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jskoll/wyrm/internal/config"
	"github.com/jskoll/wyrm/internal/editor"
	"github.com/jskoll/wyrm/internal/session"
	"github.com/jskoll/wyrm/internal/sessions"
	"github.com/jskoll/wyrm/internal/state"
	"github.com/jskoll/wyrm/internal/tmux"
	"github.com/jskoll/wyrm/internal/zoxide"
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
	// Zoxide is true for a directory zoxide knows about that has no wyrm
	// config of its own — see appendZoxideProjects. Path is empty for these;
	// starting one builds a session from the user's default config (or the
	// built-in one) rooted at Root.
	Zoxide bool
}

// listProjects annotates config.DiscoverProjects — the shared discovery rules,
// used identically by `wyrm <name>` and `wyrm list-configs` — with whether a
// session by each project's name is currently running, then folds in
// zoxide-known directories that don't already have a project of their own
// (opt-in — see appendZoxideProjects).
func listProjects(r tmux.Runner, settings *config.Settings) ([]Project, error) {
	running := map[string]string{}
	if sessions, err := sessions.List(r); err == nil {
		for _, s := range sessions {
			running[s.Name] = s.ID
		}
	}

	discovered := config.DiscoverProjects(settings)
	projects := make([]Project, 0, len(discovered))
	names := make(map[string]bool, len(discovered))
	for _, d := range discovered {
		p := Project{Name: d.Name, Path: d.Path, Shared: d.Shared, Root: d.Root, Wildcard: d.Wildcard}
		if id, ok := running[d.Name]; ok {
			p.Running, p.SessionID = true, id
		}
		projects = append(projects, p)
		names[d.Name] = true
	}
	projects = appendZoxideProjects(projects, names, running, settings)
	return projects, nil
}

// appendZoxideProjects folds zoxide's directory list into projects, skipping
// any directory whose basename collides with a project already discovered —
// a duplicate row with the same name would be confusing regardless of
// whether the two technically point at different directories, and a
// same-named wyrm project is the more specific, more likely intended match
// anyway. Absent entirely unless both settings.ZoxideEnabled() and the
// zoxide binary itself are available — see internal/zoxide's package doc.
func appendZoxideProjects(projects []Project, names map[string]bool, running map[string]string, settings *config.Settings) []Project {
	if !settings.ZoxideEnabled() || !zoxide.Available() {
		return projects
	}
	entries, err := zoxide.Query(0)
	if err != nil {
		return projects
	}
	for _, e := range entries {
		name := filepath.Base(e.Path)
		if name == "" || names[name] {
			continue
		}
		names[name] = true
		p := Project{Name: name, Root: e.Path, Zoxide: true}
		if id, ok := running[name]; ok {
			p.Running, p.SessionID = true, id
		}
		projects = append(projects, p)
	}
	return projects
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
func startProjectCmd(r tmux.Runner, settings *config.Settings, p Project) tea.Cmd {
	return func() tea.Msg {
		cfg, err := projectConfig(p)
		if err != nil {
			return projectStartedMsg{err: err}
		}
		hist, err := state.Load()
		if err != nil {
			return projectStartedMsg{err: err}
		}
		_, id, _, err := session.Create(r, cfg, io.Discard, io.Discard, session.WithHistory(hist))
		if err == nil && p.Root != "" && settings.ZoxideTrack() && zoxide.Available() {
			// Best-effort: teaching zoxide about a directory wyrm just
			// built a session for is a nice-to-have, never worth failing
			// the session build over.
			_ = zoxide.Add(p.Root)
		}
		return projectStartedMsg{sessionID: id, err: err}
	}
}

// projectConfig loads the config a project should build from: the template
// (Root-overridden) for a wildcard match, the user's default (or wyrm's
// built-in one) rooted at Root for a zoxide-only directory, or the config at
// Path for anything else.
func projectConfig(p Project) (*config.Config, error) {
	if p.Zoxide {
		cfg, err := config.LoadUserDefault()
		if err != nil {
			return nil, err
		}
		if cfg == nil {
			cfg, err = config.LoadDefault()
			if err != nil {
				return nil, err
			}
		}
		cfg.Session.Root = p.Root
		return cfg, nil
	}
	cfg, err := config.Load(p.Path)
	if err != nil {
		return nil, err
	}
	if p.Wildcard {
		// The template's own session.root (normally unset) doesn't say
		// which directory this Project stands for — only the match
		// does. See config.DiscoverWildcardProjects.
		cfg.Session.Root = p.Root
	}
	return cfg, nil
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
