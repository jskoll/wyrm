package selfupdate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallReplacesFilePreservingMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wyrm")
	if err := os.WriteFile(path, []byte("old binary"), 0o744); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := Install(path, []byte("new binary"), info.Mode()); err != nil {
		t.Fatalf("Install: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new binary" {
		t.Errorf("content = %q, want %q", data, "new binary")
	}
	newInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if newInfo.Mode() != info.Mode() {
		t.Errorf("mode = %v, want %v", newInfo.Mode(), info.Mode())
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("dir has %d entries after Install, want 1 (no leftover temp file): %v", len(entries), entries)
	}
}

func TestInstallCreatesFileIfMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wyrm")
	if err := Install(path, []byte("fresh"), 0o755); err != nil {
		t.Fatalf("Install: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "fresh" {
		t.Errorf("content = %q, want %q", data, "fresh")
	}
}
