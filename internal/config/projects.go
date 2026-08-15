package config

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
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
	// Aliases are additional exact-match names FindProject resolves to this
	// project, from the config's own session.aliases. Never set for a
	// Wildcard project — a template shared by many directories has no single
	// alias to give any one of them.
	Aliases []string
	// Root is the matched directory for a Wildcard project (absolute) and
	// empty otherwise. When set, it overrides the template config's own
	// session.root — see DiscoverWildcardProjects.
	Root string
	// Wildcard is true for a project synthesized from a [[wildcard]] pattern
	// match rather than discovered as its own file. Many Wildcard projects
	// can share the same Path (the template) while differing in Root.
	Wildcard bool
}

// nameCache memoizes ProjectName/aliases by file identity, so repeated
// discovery doesn't re-read and re-parse every config on disk.
//
// The TUI calls DiscoverProjects on a 3-second timer, and each call used to
// Load() every shared config to work out its session name — twenty projects
// meant twenty file reads and twenty TOML parses every three seconds, forever.
// Keying on (size, mtime) means an edit is still picked up on the next tick,
// which is what the timer is for.
var nameCache sync.Map // path -> nameCacheEntry

type nameCacheEntry struct {
	size    int64
	mtime   time.Time
	name    string
	aliases []string
}

// cachedProjectInfo returns ProjectName(path, shared) and the config's
// session.aliases, reusing the last result while the file is unchanged. info
// is the already-stat'ed file, since callers have had to stat it to know it
// exists.
func cachedProjectInfo(path string, shared bool, info os.FileInfo) (name string, aliases []string) {
	if v, ok := nameCache.Load(path); ok {
		e := v.(nameCacheEntry)
		if e.size == info.Size() && e.mtime.Equal(info.ModTime()) {
			return e.name, e.aliases
		}
	}
	name = ProjectName(path, shared)
	if cfg, err := Load(path); err == nil {
		aliases = cfg.Session.Aliases
	}
	nameCache.Store(path, nameCacheEntry{size: info.Size(), mtime: info.ModTime(), name: name, aliases: aliases})
	return name, aliases
}

// DiscoverProjects enumerates every config wyrm can see: the local
// .wyrm.toml/.tmuxconfig in the current directory, every "<folder>.wyrm.toml"
// in the shared config directory, and every directory matched by a
// [[wildcard]] pattern. Shared entries are sorted by path for a stable order;
// the local config, when present, comes first. settings may be nil, in which
// case only local configs are returned.
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
		name, aliases := cachedProjectInfo(path, shared, info)
		if name == "" {
			return
		}
		projects = append(projects, Project{Name: name, Path: path, Shared: shared, Aliases: aliases})
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
	projects = append(projects, DiscoverWildcardProjects(settings)...)
	return projects
}

// DiscoverWildcardProjects expands every configured [[wildcard]] pattern into
// one Project per matching directory, all sharing that wildcard's template
// config file. Unlike a project discovered by DiscoverProjects' normal walk,
// a Wildcard project's identity is the (template path, matched directory)
// pair rather than the file alone — many directories legitimately share one
// template — so it is never deduplicated against the plain file-based scan.
//
// Root is the matched directory, not the template's own session.root — a
// wildcard template conventionally sets that to "." as a placeholder (see
// Wildcard.Config), since callers building a session for one of these
// Projects always override cfg.Session.Root with it, config.Load alone
// having no way to know which directory this particular Project stands for.
//
// Unlike file-based discovery, this re-globs and re-stats every pattern on
// every call — there is no (size, mtime) identity to cache against, since a
// wildcard project isn't a file. Fine at the scale this feature targets
// (tens to low hundreds of directories); not optimized further here.
func DiscoverWildcardProjects(settings *Settings) []Project {
	if settings == nil {
		return nil
	}
	var out []Project
	for _, wc := range settings.Wildcard {
		if wc.Pattern == "" || wc.Config == "" {
			continue
		}
		configPath, err := ExpandPath(wc.Config)
		if err != nil {
			continue
		}
		dirs, err := matchWildcardDirs(wc.Pattern)
		if err != nil {
			continue
		}
		for _, dir := range dirs {
			out = append(out, Project{
				Name:     filepath.Base(dir),
				Path:     configPath,
				Root:     dir,
				Wildcard: true,
			})
		}
	}
	return out
}

// matchWildcardDirs resolves a wildcard pattern to the absolute directories
// it matches. A trailing "/**" matches every directory nested at any depth
// under the base path (not the base itself); anything else is a plain
// filepath.Glob, matching one path segment per "*" the way DiscoverProjects'
// own shared-directory glob does.
func matchWildcardDirs(pattern string) ([]string, error) {
	expanded, err := ExpandPath(pattern)
	if err != nil {
		return nil, err
	}
	if base, ok := strings.CutSuffix(expanded, "/**"); ok {
		var dirs []string
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				// An unreadable subdirectory shouldn't cost every sibling its
				// match, so skip it rather than aborting the whole walk —
				// returning nil from a WalkDirFunc means exactly that, per
				// its documented contract, not "swallow the error".
				return nil //nolint:nilerr
			}
			if path != base && d.IsDir() {
				name := d.Name()
				if strings.HasPrefix(name, ".") || isIgnoredWildcardDir(name) {
					return filepath.SkipDir
				}
				dirs = append(dirs, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		return dirs, nil
	}

	matches, err := filepath.Glob(expanded)
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, m := range matches {
		if info, err := os.Stat(m); err == nil && info.IsDir() {
			abs, err := filepath.Abs(m)
			if err != nil {
				abs = m
			}
			dirs = append(dirs, abs)
		}
	}
	return dirs, nil
}

var ignoredWildcardDirNames = map[string]bool{
	"node_modules": true,
	"vendor":       true,
	"target":       true,
	"dist":         true,
	"build":        true,
	"venv":         true,
	"__pycache__":  true,
}

func isIgnoredWildcardDir(name string) bool {
	return ignoredWildcardDirNames[name]
}

// FindProject returns the discoverable project whose session name is name,
// or — failing that — whose session.aliases contains name. An exact project
// name always wins over an alias collision, so `wyrm <name>` stays
// deterministic even if a project happens to alias another's name.
func FindProject(settings *Settings, name string) (Project, bool) {
	projects := DiscoverProjects(settings)
	for _, p := range projects {
		if p.Name == name {
			return p, true
		}
	}
	for _, p := range projects {
		if slices.Contains(p.Aliases, name) {
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
