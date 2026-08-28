package config

import (
	"strconv"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

// Every value the wizard writes goes through tomlQuote, so whatever it emits
// has to parse back as the same string. strconv.Quote did not: TOML has no
// \a, \v or \xNN, so a bell character or an invalid UTF-8 byte in a session
// name produced a config the loader rejected — after the user had answered
// every prompt.
func TestTomlQuoteRoundTrips(t *testing.T) {
	for _, in := range []string{
		"plain",
		`with "quotes"`,
		`back\slash`,
		"tab\there",
		"newline\nhere",
		"carriage\rreturn",
		"form\ffeed",
		"back\bspace",
		"bell\a",        // strconv.Quote emits \a — not legal TOML
		"vertical\vtab", // likewise \v
		"ctrl\x01char",  // strconv.Quote emits \x01 — not legal TOML
		"del\x7f",
		"unicode ✓ 日本語",
		"emoji 🐉",
		"",
	} {
		t.Run(strconv.Quote(in), func(t *testing.T) {
			doc := "name = " + tomlQuote(in) + "\n"
			var got struct {
				Name string `toml:"name"`
			}
			if err := toml.Unmarshal([]byte(doc), &got); err != nil {
				t.Fatalf("tomlQuote(%q) produced %s which does not parse: %v", in, doc, err)
			}
			if got.Name != in {
				t.Errorf("round trip: got %q, want %q (emitted %s)", got.Name, in, doc)
			}
		})
	}
}

// A byte sequence that is not valid UTF-8 cannot be a TOML string at all, so
// it is replaced rather than emitted — the wizard still produces a loadable
// config instead of failing at the final step.
func TestTomlQuoteHandlesInvalidUTF8(t *testing.T) {
	in := "bad" + string([]byte{0xff, 0xfe}) + "end"
	doc := "name = " + tomlQuote(in) + "\n"
	var got struct {
		Name string `toml:"name"`
	}
	if err := toml.Unmarshal([]byte(doc), &got); err != nil {
		t.Fatalf("emitted %s which does not parse: %v", doc, err)
	}
	if !strings.HasPrefix(got.Name, "bad") || !strings.HasSuffix(got.Name, "end") {
		t.Errorf("surrounding text should survive, got %q", got.Name)
	}
	if strings.ContainsRune(got.Name, 0xff) {
		t.Error("invalid byte should not have been emitted verbatim")
	}
}

// The generated config as a whole must load, not just individual values —
// `wyrm init` validates its own output, so a bad escape anywhere fails the
// wizard after every prompt has been answered.
func TestGeneratedConfigWithAwkwardInputLoads(t *testing.T) {
	out := GenerateCustomConfig("bell\aname", "/tmp/root", []WindowSpec{{
		Name:     "win\vdow",
		Preset:   PresetTwoPaneVertical,
		Commands: []string{"echo \x01hi", `say "hi"`},
	}})
	if _, _, err := Decode([]byte(out)); err != nil {
		t.Fatalf("generated config does not load: %v\n%s", err, out)
	}
}

// The starter templates take the same path through tomlQuote.
func TestTemplateConfigWithAwkwardInputLoads(t *testing.T) {
	out, err := GetTemplate("minimal", "bell\aname", "/tmp/root")
	if err != nil {
		t.Fatalf("GetTemplate: %v", err)
	}
	if _, _, err := Decode([]byte(out)); err != nil {
		t.Fatalf("template config does not load: %v\n%s", err, out)
	}
}
