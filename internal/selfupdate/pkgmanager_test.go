package selfupdate

import (
	"path/filepath"
	"testing"
)

func TestManagedDetectsHomebrewByPath(t *testing.T) {
	path := filepath.FromSlash("/opt/homebrew/Cellar/wyrm/0.6.2/bin/wyrm")
	manager, hint, ok := Managed(path)
	if !ok {
		t.Fatal("Managed: want true for a Cellar path, got false")
	}
	if manager != "Homebrew" {
		t.Errorf("manager = %q, want Homebrew", manager)
	}
	if hint == "" {
		t.Error("hint is empty")
	}
}

func TestManagedFalseForOrdinaryPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wyrm")
	if _, _, ok := Managed(path); ok {
		t.Error("Managed: want false for a plain temp-dir binary, got true")
	}
}
