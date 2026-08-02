package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingFileReturnsEmptyStore(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	s, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.Started("/some/project") {
		t.Error("Started on a fresh store = true, want false")
	}
}

func TestMarkStartedPersistsAcrossLoad(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	s, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := s.MarkStarted("/proj/a"); err != nil {
		t.Fatalf("MarkStarted: %v", err)
	}

	s2, err := Load()
	if err != nil {
		t.Fatalf("second Load: %v", err)
	}
	if !s2.Started("/proj/a") {
		t.Error("Started(/proj/a) after MarkStarted+reload = false, want true")
	}
	if s2.Started("/proj/b") {
		t.Error("Started(/proj/b) = true, want false (never marked)")
	}
}

func TestMarkStartedIsIdempotent(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	s, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := s.MarkStarted("/proj/a"); err != nil {
		t.Fatalf("MarkStarted: %v", err)
	}
	if err := s.MarkStarted("/proj/a"); err != nil {
		t.Fatalf("second MarkStarted: %v", err)
	}
	if !s.Started("/proj/a") {
		t.Error("Started after two MarkStarted calls = false, want true")
	}
}

func TestNilStoreIsSafe(t *testing.T) {
	var s *Store
	if s.Started("/proj/a") {
		t.Error("nil Store Started = true, want false")
	}
	if err := s.MarkStarted("/proj/a"); err != nil {
		t.Errorf("nil Store MarkStarted: %v, want nil", err)
	}
}

func TestStartedEmptyDirAlwaysFalse(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.Started("") {
		t.Error(`Started("") = true, want false`)
	}
	if err := s.MarkStarted(""); err != nil {
		t.Fatalf("MarkStarted(\"\"): %v", err)
	}
	if s.Started("") {
		t.Error(`Started("") after MarkStarted("") = true, want false`)
	}
}

func TestLoadParsesExistingFile(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)

	dir := filepath.Join(xdg, "wyrm")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "started = [\"/proj/a\", \"/proj/b\"]\n"
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !s.Started("/proj/a") || !s.Started("/proj/b") {
		t.Error("Load did not pick up the pre-existing started entries")
	}
}

func TestLoadParseError(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)

	dir := filepath.Join(xdg, "wyrm")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte("[not valid"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(); err == nil {
		t.Error("Load with malformed state file: want error, got nil")
	}
}
