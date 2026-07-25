package config

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Project is a discoverable wyrm config: the session name it would produce and
// where the file lives. It is the unit both the TUI's Projects panel and
// `wyrm <name>` work in, so the discovery rules live here rather than being
// implemented once per caller.
type Project struct {
	Name   string
	Path   string
	Shared bool
}

// DiscoverProjects enumerates every config wyrm can see: the local
// .wyrm.toml/.tmuxconfig in the current directory, plus every
// "<folder>.wyrm.toml" in the shared config directory. Shared entries are
// sorted by path for a stable order; the local config, when present, comes
// first. settings may be nil, in which case only local configs are returned.
func DiscoverProjects(settings *Settings) []Project {
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
		name := ProjectName(path, shared)
		if name == "" {
			return
		}
		projects = append(projects, Project{Name: name, Path: path, Shared: shared})
	}

	for _, name := range []string{DefaultFileName, LegacyFileName} {
		if _, err := os.Stat(name); err == nil {
			add(name, false)
		}
	}
	if settings != nil {
		if dir, err := settings.ResolvedSharedDir(); err == nil {
			matches, _ := filepath.Glob(filepath.Join(dir, "*"+DefaultFileName))
			sort.Strings(matches)
			for _, m := range matches {
				add(m, true)
			}
		}
	}
	return projects
}

// FindProject returns the discoverable project whose session name is name.
func FindProject(settings *Settings, name string) (Project, bool) {
	for _, p := range DiscoverProjects(settings) {
		if p.Name == name {
			return p, true
		}
	}
	return Project{}, false
}

// ProjectName is the session name a config produces: its explicit
// session.name, else the name derived from its resolved root, else the
// filename with the .wyrm.toml suffix stripped.
//
// A shared config deliberately skips the root-derived branch. Its root is
// relative to the shared directory, not to any project, so deriving a name
// from it would name every shared project after the shared folder.
func ProjectName(path string, shared bool) string {
	if cfg, err := Load(path); err == nil {
		if cfg.Session.Name != "" {
			return cfg.Session.Name
		}
		if !shared {
			if name, _, err := cfg.Session.Resolve(cfg.Dir()); err == nil {
				return name
			}
		}
	}
	base := filepath.Base(path)
	if shared {
		return strings.TrimSuffix(base, DefaultFileName)
	}
	if cwd, err := os.Getwd(); err == nil {
		return filepath.Base(cwd)
	}
	return base
}

// CheckSharedRoot reports the problem with a shared config whose session.root
// is relative, and whether there is one. A shared config lives in the shared
// directory rather than in the project, so a relative root — "." above all —
// resolves against the shared directory and builds a session rooted in the
// wrong place. Callers warn rather than refuse: the session still builds, and
// some configs legitimately set only a name.
func CheckSharedRoot(p Project) (string, bool) {
	if !p.Shared {
		return "", false
	}
	cfg, err := Load(p.Path)
	if err != nil {
		return "", false
	}
	root := cfg.Session.Root
	if root == "" || filepath.IsAbs(root) || strings.HasPrefix(root, "~") || strings.Contains(root, "$") {
		return "", false
	}
	return "shared config " + p.Path + " has a relative session.root (" + root +
		"), which resolves against the shared directory — use an absolute path or ~/...", true
}
