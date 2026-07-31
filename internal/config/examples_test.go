package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestShippedExamplesLoad parses every config in examples/. They're presented
// in the docs as copy-paste-ready starting points, so a schema change that
// invalidates one has to fail here rather than in a user's terminal.
func TestShippedExamplesLoad(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "examples", "*.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no examples found — did examples/ move?")
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			cfg, err := Load(path)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if len(cfg.Windows) == 0 {
				t.Error("example defines no windows")
			}
			// The examples are copy-paste starting points, so a key that no
			// longer exists (or never did) has to fail here rather than be
			// silently ignored in someone's terminal.
			for _, w := range cfg.Warnings() {
				if strings.Contains(w, "unknown key") {
					t.Errorf("example has an unknown key: %s", w)
				}
			}
		})
	}
}

// TestShippedExamplesPreferSplits keeps the examples honest about the
// deprecation: only basic.wyrm.toml may use the flat panes list, and it has to
// say so, since docs/examples.md tells readers to copy these in verbatim.
func TestShippedExamplesPreferSplits(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "examples", "*.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		name := filepath.Base(path)
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		usesPanes := false
		for _, w := range cfg.Windows {
			if len(w.Panes) > 0 {
				usesPanes = true
			}
		}
		if !usesPanes {
			continue
		}
		if name != "basic.wyrm.toml" {
			t.Errorf("%s uses the deprecated flat panes list; convert it to splits", name)
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "LEGACY") {
			t.Errorf("%s uses the deprecated panes list without saying so", name)
		}
	}
}
