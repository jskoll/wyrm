package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSettingsDefaultsWhenMissing(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	s, err := LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if s.Storage != StorageLocal {
		t.Errorf("Storage = %q, want %q", s.Storage, StorageLocal)
	}
}

func TestLoadSettingsParsesFile(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)

	dir := filepath.Join(xdg, "wyrm")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "storage = \"shared\"\nshared_dir = \"/custom/dir\"\n"
	if err := os.WriteFile(filepath.Join(dir, SettingsFileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if s.Storage != StorageShared {
		t.Errorf("Storage = %q, want %q", s.Storage, StorageShared)
	}
	if s.SharedDir != "/custom/dir" {
		t.Errorf("SharedDir = %q, want /custom/dir", s.SharedDir)
	}
}

func TestLoadSettingsInvalidStorage(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)

	dir := filepath.Join(xdg, "wyrm")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, SettingsFileName), []byte(`storage = "nope"`), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadSettings(); err == nil {
		t.Error("LoadSettings with invalid storage: want error, got nil")
	}
}

func TestResolvedSharedDirDefaultExpandsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	s := &Settings{}
	got, err := s.resolvedSharedDir()
	if err != nil {
		t.Fatalf("resolvedSharedDir: %v", err)
	}
	want := filepath.Join(home, ".config", "wyrm", "settings")
	if got != want {
		t.Errorf("resolvedSharedDir = %q, want %q", got, want)
	}
}

func TestSharedConfigPath(t *testing.T) {
	s := &Settings{SharedDir: "/shared"}
	got, err := s.SharedConfigPath("/home/user/myproject")
	if err != nil {
		t.Fatalf("SharedConfigPath: %v", err)
	}
	want := "/shared/myproject.wyrm.toml"
	if got != want {
		t.Errorf("SharedConfigPath = %q, want %q", got, want)
	}
}

func TestDiscoverGlobalSharedMode(t *testing.T) {
	sharedDir := t.TempDir()
	projectDir := t.TempDir()
	chdir(t, projectDir)

	settings := &Settings{Storage: StorageShared, SharedDir: sharedDir}

	// No shared file yet: falls back to local discovery, which also fails.
	if _, err := DiscoverGlobal(settings); err == nil {
		t.Error("DiscoverGlobal with nothing present: want error, got nil")
	}

	// Local file present: falls back to it.
	if err := os.WriteFile(DefaultFileName, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := DiscoverGlobal(settings); err != nil || got != DefaultFileName {
		t.Errorf("DiscoverGlobal = %q, %v, want %q, nil", got, err, DefaultFileName)
	}

	// Shared file present: preferred over the local one.
	folderName := filepath.Base(projectDir)
	sharedPath := filepath.Join(sharedDir, folderName+DefaultFileName)
	if err := os.WriteFile(sharedPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := DiscoverGlobal(settings); err != nil || got != sharedPath {
		t.Errorf("DiscoverGlobal = %q, %v, want %q, nil", got, err, sharedPath)
	}
}

func TestDiscoverGlobalLocalMode(t *testing.T) {
	chdir(t, t.TempDir())
	if err := os.WriteFile(DefaultFileName, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	settings := &Settings{Storage: StorageLocal}
	if got, err := DiscoverGlobal(settings); err != nil || got != DefaultFileName {
		t.Errorf("DiscoverGlobal = %q, %v, want %q, nil", got, err, DefaultFileName)
	}
}
