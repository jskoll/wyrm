package config

import (
	"path/filepath"
	"strings"
)

// RootNeedsAbsolute reports whether root is a relative path that would
// resolve against the wrong directory once the config that carries it is
// stored somewhere other than the project it describes — see CheckSharedRoot
// and Session.Resolve. An unset root is left alone, matching CheckSharedRoot:
// some shared configs legitimately set only a name.
func RootNeedsAbsolute(root string) bool {
	if root == "" || filepath.IsAbs(root) {
		return false
	}
	return !strings.HasPrefix(root, "~") && !strings.Contains(root, "$")
}

// RewriteSessionRoot returns data with the [session] table's root key set to
// newRoot — replacing an existing root line, or inserting one right after the
// [session] header when the table has none — leaving every other line
// (comments, formatting, unrelated tables) untouched.
//
// It exists for migrateConfig: moving a config file into shared storage
// changes what a relative session.root means, and a full decode/re-encode
// round trip through the TOML library would silently drop the user's
// comments while fixing that.
func RewriteSessionRoot(data []byte, newRoot string) []byte {
	lines := strings.Split(string(data), "\n")
	insertAt := -1
	inSession := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			if trimmed == "[session]" {
				inSession = true
				insertAt = i + 1
				continue
			}
			if inSession {
				break // left [session] without finding a root key
			}
			continue
		}
		if !inSession {
			continue
		}
		if isRootKeyLine(trimmed) {
			lines[i] = "root = " + tomlQuote(newRoot)
			return []byte(strings.Join(lines, "\n"))
		}
	}
	if insertAt < 0 {
		// No [session] table found at all — nothing sensible to patch.
		return data
	}
	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:insertAt]...)
	out = append(out, "root = "+tomlQuote(newRoot))
	out = append(out, lines[insertAt:]...)
	return []byte(strings.Join(out, "\n"))
}

// isRootKeyLine reports whether trimmed (a line with leading/trailing space
// already stripped) assigns the "root" key, as opposed to some other key that
// merely starts with "root" (e.g. a hypothetical "root_dir").
func isRootKeyLine(trimmed string) bool {
	if strings.HasPrefix(trimmed, "#") {
		return false
	}
	rest, ok := strings.CutPrefix(trimmed, "root")
	if !ok {
		return false
	}
	rest = strings.TrimSpace(rest)
	return strings.HasPrefix(rest, "=")
}
