package config

import (
	"testing"
)

func TestStarterTemplatesValid(t *testing.T) {
	for _, name := range AvailableTemplates() {
		t.Run(name, func(t *testing.T) {
			content, err := GetTemplate(name, "test-session", ".")
			if err != nil {
				t.Fatalf("GetTemplate(%q) failed: %v", name, err)
			}
			cfg, unknown, err := Decode([]byte(content))
			if err != nil {
				t.Fatalf("Decode(%q) failed: %v\nContent:\n%s", name, err, content)
			}
			if len(unknown) > 0 {
				t.Fatalf("Decode(%q) had unknown keys: %v", name, unknown)
			}
			if err := cfg.Validate(); err != nil {
				t.Fatalf("Validate(%q) failed: %v\nContent:\n%s", name, err, content)
			}
			if len(cfg.Warnings()) > 0 {
				t.Fatalf("Validate(%q) produced warnings: %v", name, cfg.Warnings())
			}
			if cfg.Session.Name != "test-session" {
				t.Errorf("expected session.name %q, got %q", "test-session", cfg.Session.Name)
			}
			if cfg.Session.Root != "." {
				t.Errorf("expected session.root %q, got %q", ".", cfg.Session.Root)
			}
			if len(cfg.Windows) == 0 {
				t.Errorf("expected windows, got 0")
			}
		})
	}
}

func TestTemplateAliases(t *testing.T) {
	cases := []struct {
		alias    string
		wantName string
	}{
		{"nodejs", "node"},
		{"javascript", "node"},
		{"js", "node"},
		{"ts", "node"},
		{"typescript", "node"},
		{"py", "python"},
		{"golang", "go"},
		{"rs", "rust"},
		{"mono", "monorepo"},
		{"workspace", "monorepo"},
		{"basic", "minimal"},
		{"default", "minimal"},
	}

	for _, tc := range cases {
		t.Run(tc.alias, func(t *testing.T) {
			tmpl, ok := FindTemplate(tc.alias)
			if !ok {
				t.Fatalf("FindTemplate(%q) returned not found", tc.alias)
			}
			if tmpl.Name != tc.wantName {
				t.Errorf("FindTemplate(%q) = %q, want %q", tc.alias, tmpl.Name, tc.wantName)
			}
		})
	}
}

func TestGetTemplateUnknown(t *testing.T) {
	_, err := GetTemplate("nonexistent", "session", ".")
	if err == nil {
		t.Fatal("expected error for nonexistent template, got nil")
	}
}

func TestGenerateCustomConfigPresets(t *testing.T) {
	presets := []struct {
		name     string
		preset   int
		commands []string
	}{
		{"single", PresetSingle, []string{"nvim"}},
		{"two-pane-v", PresetTwoPaneVertical, []string{"nvim", "npm test"}},
		{"two-pane-h", PresetTwoPaneHorizontal, []string{"nvim", "pytest"}},
		{"three-pane-stack", PresetThreePaneEditorStack, []string{"nvim", "npm run dev", "npm test"}},
		{"three-pane-main-h", PresetThreePaneMainHorizontal, []string{"nvim", "docker compose up", "cargo test"}},
	}

	for _, tc := range presets {
		t.Run(tc.name, func(t *testing.T) {
			windows := []WindowSpec{
				{
					Name:     tc.name,
					Preset:   tc.preset,
					Commands: tc.commands,
				},
			}
			content := GenerateCustomConfig("custom-app", "~/code/custom-app", windows)
			cfg, unknown, err := Decode([]byte(content))
			if err != nil {
				t.Fatalf("Decode failed: %v\nContent:\n%s", err, content)
			}
			if len(unknown) > 0 {
				t.Fatalf("Decode had unknown keys: %v", unknown)
			}
			if err := cfg.Validate(); err != nil {
				t.Fatalf("Validate failed: %v\nContent:\n%s", err, content)
			}
			if len(cfg.Warnings()) > 0 {
				t.Fatalf("Validate produced warnings: %v", cfg.Warnings())
			}
			if cfg.Session.Name != "custom-app" {
				t.Errorf("expected session.name %q, got %q", "custom-app", cfg.Session.Name)
			}
			if cfg.Session.Root != "~/code/custom-app" {
				t.Errorf("expected session.root %q, got %q", "~/code/custom-app", cfg.Session.Root)
			}
			if len(cfg.Windows) != 1 {
				t.Fatalf("expected 1 window, got %d", len(cfg.Windows))
			}
		})
	}
}

func TestGenerateCustomConfigMultipleWindows(t *testing.T) {
	windows := []WindowSpec{
		{
			Name:     "editor",
			Preset:   PresetThreePaneEditorStack,
			Commands: []string{"$EDITOR .", "npm test -- --watch", "npm run dev"},
		},
		{
			Name:     "database",
			Preset:   PresetTwoPaneHorizontal,
			Commands: []string{"psql", "redis-cli"},
		},
		{
			Name:     "shell",
			Preset:   PresetSingle,
			Commands: []string{""},
		},
	}
	content := GenerateCustomConfig("multi-app", ".", windows)
	cfg, unknown, err := Decode([]byte(content))
	if err != nil {
		t.Fatalf("Decode failed: %v\nContent:\n%s", err, content)
	}
	if len(unknown) > 0 {
		t.Fatalf("Decode had unknown keys: %v", unknown)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate failed: %v\nContent:\n%s", err, content)
	}
	if len(cfg.Warnings()) > 0 {
		t.Fatalf("Validate produced warnings: %v", cfg.Warnings())
	}
	if len(cfg.Windows) != 3 {
		t.Fatalf("expected 3 windows, got %d", len(cfg.Windows))
	}
}
