package config

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
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

// nameCache memoizes ProjectName by file identity, so repeated discovery
// doesn't re-read and re-parse every config on disk.
//
// The TUI calls DiscoverProjects on a 3-second timer, and each call used to
// Load() every shared config to work out its session name — twenty projects
// meant twenty file reads and twenty TOML parses every three seconds, forever.
// Keying on (size, mtime) means an edit is still picked up on the next tick,
// which is what the timer is for.
var nameCache sync.Map // path -> nameCacheEntry

type nameCacheEntry struct {
	size  int64
	mtime time.Time
	name  string
}

// cachedProjectName returns ProjectName(path, shared), reusing the last result
// while the file is unchanged. info is the already-stat'ed file, since callers
// have had to stat it to know it exists.
func cachedProjectName(path string, shared bool, info os.FileInfo) string {
	if v, ok := nameCache.Load(path); ok {
		e := v.(nameCacheEntry)
		if e.size == info.Size() && e.mtime.Equal(info.ModTime()) {
			return e.name
		}
	}
	name := ProjectName(path, shared)
	nameCache.Store(path, nameCacheEntry{size: info.Size(), mtime: info.ModTime(), name: name})
	return name
}

// DiscoverProjects enumerates every config wyrm can see: the local
// .wyrm.toml/.tmuxconfig in the current directory, plus every
// "<folder>.wyrm.toml" in the shared config directory. Shared entries are
// sorted by path for a stable order; the local config, when present, comes
// first. settings may be nil, in which case only local configs are returned.
func DiscoverProjects(settings *Settings) []Project {
	var projects []Project
	seen := map[string]bool{}
	add := func(path string, shared bool, info os.FileInfo) {
		abs, err := filepath.Abs(path)
		if err != nil {
			abs = path
		}
		if seen[abs] {
			return
		}
		seen[abs] = true
		name := cachedProjectName(path, shared, info)
		if name == "" {
			return
		}
		projects = append(projects, Project{Name: name, Path: path, Shared: shared})
	}

	for _, name := range []string{DefaultFileName, LegacyFileName} {
		if info, err := os.Stat(name); err == nil {
			add(name, false, info)
		}
	}
	if settings != nil {
		if dir, err := settings.ResolvedSharedDir(); err == nil {
			matches, _ := filepath.Glob(filepath.Join(dir, "*"+DefaultFileName))
			sort.Strings(matches)
			for _, m := range matches {
				info, err := os.Stat(m)
				if err != nil {
					continue
				}
				add(m, true, info)
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
func CheckSharedRoot(p Project, cfg *Config) (string, bool) {
	if !p.Shared || cfg == nil {
		return "", false
	}
	root := cfg.Session.Root
	if root == "" || filepath.IsAbs(root) || strings.HasPrefix(root, "~") || strings.Contains(root, "$") {
		return "", false
	}
	return "shared config " + p.Path + " has a relative session.root (" + root +
		"), which resolves against the shared directory — use an absolute path or ~/...", true
}
