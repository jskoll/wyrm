package tui

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/jskoll/wyrm/internal/config"
)

// role pairs a theme field with the key that names it in the theme file, so
// validation and error messages both work from one list.
type role struct {
	name  string
	value *string
}

func (t *Theme) roles() []role {
	return []role{
		{"accent", &t.Accent},
		{"subtle", &t.Subtle},
		{"filter", &t.Filter},
		{"selected", &t.Selected},
		{"text", &t.Text},
		{"trail", &t.Trail},
		{"index", &t.Index},
		{"active", &t.Active},
		{"error", &t.Error},
	}
}

// LoadTheme reads the user's theme file (config.ThemePath) and layers it over
// the built-in default, returning the default unchanged when no file exists.
//
// Anything the file gets wrong is an error rather than a silent fallback: the
// TUI takes the whole screen the moment it starts, so a warning printed to
// stderr would be wiped before it could be read, and a theme that quietly
// ignored half its entries would look like a wyrm bug rather than a typo.
func LoadTheme() (Theme, error) {
	path, err := config.ThemePath()
	if err != nil {
		return DefaultTheme(), err
	}
	return loadThemeFile(path)
}

func loadThemeFile(path string) (Theme, error) {
	def := DefaultTheme()

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return def, nil
	}
	if err != nil {
		return def, err
	}

	var t Theme
	dec := toml.NewDecoder(bytes.NewReader(data))
	// Strict: a mistyped role would otherwise be dropped without a word, and
	// the colors it names are the whole point of the file.
	dec.DisallowUnknownFields()
	if err := dec.Decode(&t); err != nil {
		// go-toml's strict error reads "fields in the document are missing in
		// the target struct" and names neither the file nor the offending key
		// — useless for the typo it exists to catch.
		var missing *toml.StrictMissingError
		if errors.As(err, &missing) {
			return def, fmt.Errorf("%s: unknown theme role %s (valid: %s)",
				path, unknownKeys(missing), strings.Join(roleNames(), ", "))
		}
		return def, fmt.Errorf("parsing %s: %w", path, err)
	}

	// An unset role keeps its default, so a file can name one color without
	// blanking the other eight.
	defaults := def.roles()
	for i, r := range t.roles() {
		if *r.value == "" {
			*r.value = *defaults[i].value
			continue
		}
		if !validHex(*r.value) {
			return def, fmt.Errorf("%s: %s must be a hex color like %q, got %q", path, r.name, def.Accent, *r.value)
		}
	}
	return t, nil
}

func roleNames() []string {
	var t Theme
	names := make([]string, 0, len(t.roles()))
	for _, r := range t.roles() {
		names = append(names, r.name)
	}
	return names
}

// unknownKeys renders the keys go-toml rejected, quoted, for an error message.
func unknownKeys(e *toml.StrictMissingError) string {
	keys := make([]string, 0, len(e.Errors))
	for _, d := range e.Errors {
		keys = append(keys, strconv.Quote(strings.Join(d.Key(), ".")))
	}
	return strings.Join(keys, ", ")
}

// validHex reports whether s is "#rgb" or "#rrggbb". lipgloss treats anything
// it can't parse as "no color", which renders as an invisible role rather
// than as a complaint — so the check happens here instead.
func validHex(s string) bool {
	if len(s) != 4 && len(s) != 7 {
		return false
	}
	if s[0] != '#' {
		return false
	}
	for _, c := range s[1:] {
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}
