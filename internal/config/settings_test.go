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

// TestResolvedSharedDirDefaultUsesHome covers the no-XDG case. XDG_CONFIG_HOME
// has to be cleared explicitly: CI runners set it, and the default shared dir
// now sits next to the settings file rather than being hardcoded under
// ~/.config, so it honors XDG the same way SettingsPath does.
func TestResolvedSharedDirDefaultUsesHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	s := &Settings{}
	got, err := s.ResolvedSharedDir()
	if err != nil {
		t.Fatalf("ResolvedSharedDir: %v", err)
	}
	want := filepath.Join(home, ".config", "wyrm", "settings")
	if got != want {
		t.Errorf("ResolvedSharedDir = %q, want %q", got, want)
	}
}

// TestResolvedSharedDirDefaultHonorsXDG is the other half: settings and shared
// configs must resolve under the same root. They didn't — the settings file
// followed XDG_CONFIG_HOME while the shared directory was hardcoded to
// ~/.config/wyrm/settings, so a user with XDG set had wyrm read its
// preferences from one place and look for project configs in another.
func TestResolvedSharedDirDefaultHonorsXDG(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("HOME", t.TempDir()) // deliberately different
	t.Setenv("XDG_CONFIG_HOME", xdg)

	s := &Settings{}
	got, err := s.ResolvedSharedDir()
	if err != nil {
		t.Fatalf("ResolvedSharedDir: %v", err)
	}
	if want := filepath.Join(xdg, "wyrm", "settings"); got != want {
		t.Errorf("ResolvedSharedDir = %q, want %q", got, want)
	}

	// And it must sit alongside the settings file, not merely under the same root.
	settings, err := SettingsPath()
	if err != nil {
		t.Fatalf("SettingsPath: %v", err)
	}
	if filepath.Dir(settings) != filepath.Dir(got) {
		t.Errorf("shared dir %q is not alongside the settings file %q", got, settings)
	}
}

// The theme file has to land next to the settings file under the same
// $XDG_CONFIG_HOME rules, or a user who moved their config root would edit a
// theme wyrm never reads.
func TestThemePathHonorsXDG(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("HOME", t.TempDir()) // deliberately different
	t.Setenv("XDG_CONFIG_HOME", xdg)

	got, err := ThemePath()
	if err != nil {
		t.Fatalf("ThemePath: %v", err)
	}
	if want := filepath.Join(xdg, "wyrm", ThemeFileName); got != want {
		t.Errorf("ThemePath = %q, want %q", got, want)
	}

	settings, err := SettingsPath()
	if err != nil {
		t.Fatalf("SettingsPath: %v", err)
	}
	if filepath.Dir(got) != filepath.Dir(settings) {
		t.Errorf("theme file %q is not alongside the settings file %q", got, settings)
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

func TestLoadUserDefaultMissingReturnsNil(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	cfg, err := LoadUserDefault()
	if err != nil {
		t.Fatalf("LoadUserDefault: %v", err)
	}
	if cfg != nil {
		t.Errorf("LoadUserDefault = %+v, want nil when no override file exists", cfg)
	}
}

func TestLoadUserDefaultPresent(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)

	dir := filepath.Join(xdg, "wyrm")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "[session]\nname = \"my-default\"\nroot = \".\"\n\n[[windows]]\nname = \"main\"\n"
	if err := os.WriteFile(filepath.Join(dir, UserDefaultFileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadUserDefault()
	if err != nil {
		t.Fatalf("LoadUserDefault: %v", err)
	}
	if cfg == nil || cfg.Session.Name != "my-default" {
		t.Errorf("LoadUserDefault = %+v, want session.name = my-default", cfg)
	}
}

func TestLoadUserDefaultInvalid(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)

	dir := filepath.Join(xdg, "wyrm")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, UserDefaultFileName), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadUserDefault(); err == nil {
		t.Error("LoadUserDefault with invalid override: want error, got nil")
	}
}

func TestEditTargetDiscoversExisting(t *testing.T) {
	chdir(t, t.TempDir())
	if err := os.WriteFile(DefaultFileName, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	path, exists, err := EditTarget(&Settings{Storage: StorageLocal})
	if err != nil {
		t.Fatalf("EditTarget: %v", err)
	}
	if !exists {
		t.Error("exists = false, want true for a discovered local config")
	}
	if path != DefaultFileName {
		t.Errorf("path = %q, want %q", path, DefaultFileName)
	}
}

func TestEditTargetCreatesLocal(t *testing.T) {
	chdir(t, t.TempDir())

	path, exists, err := EditTarget(&Settings{Storage: StorageLocal})
	if err != nil {
		t.Fatalf("EditTarget: %v", err)
	}
	if exists {
		t.Error("exists = true, want false when nothing is present yet")
	}
	if path != DefaultFileName {
		t.Errorf("path = %q, want %q", path, DefaultFileName)
	}
}

func TestEditTargetCreatesShared(t *testing.T) {
	sharedDir := t.TempDir()
	projectDir := t.TempDir()
	chdir(t, projectDir)

	settings := &Settings{Storage: StorageShared, SharedDir: sharedDir}
	path, exists, err := EditTarget(settings)
	if err != nil {
		t.Fatalf("EditTarget: %v", err)
	}
	if exists {
		t.Error("exists = true, want false when nothing is present yet")
	}
	want := filepath.Join(sharedDir, filepath.Base(projectDir)+DefaultFileName)
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
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

// The [tui] section is optional in every direction: an absent file, an absent
// section, and an absent key all have to land on the documented defaults, which
// is why the bool fields are pointers.
func TestTUISettingsDefaults(t *testing.T) {
	var nilSettings *Settings
	for name, s := range map[string]*Settings{
		"nil settings":     nilSettings,
		"empty settings":   {},
		"no [tui] section": {Storage: StorageLocal},
	} {
		t.Run(name, func(t *testing.T) {
			if !s.MouseEnabled() {
				t.Error("MouseEnabled() = false, want true by default")
			}
			if !s.AgentEnabled() {
				t.Error("AgentEnabled() = false, want true by default")
			}
			if got := s.AgentCommands(); got != nil {
				t.Errorf("AgentCommands() = %v, want nil (the package default)", got)
			}
		})
	}
}

func TestLoadSettingsParsesTUISection(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	dir := filepath.Join(xdg, "wyrm")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "storage = \"local\"\n\n[tui]\nmouse = false\n\n[tui.agent]\nenabled = false\ncommands = [\"claude\", \"aider\"]\n"
	if err := os.WriteFile(filepath.Join(dir, SettingsFileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.MouseEnabled() {
		t.Error("mouse = false in the file should disable the mouse")
	}
	if s.AgentEnabled() {
		t.Error("enabled = false in the file should disable agent detection")
	}
	if got := s.AgentCommands(); len(got) != 2 || got[0] != "claude" || got[1] != "aider" {
		t.Errorf("AgentCommands() = %v, want [claude aider]", got)
	}
}
