package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultMapsFeaturedServersToThemselves(t *testing.T) {
	cfg := Default()

	if len(cfg.Mappings) != len(featuredServers) {
		t.Fatalf("mappings = %d, want %d", len(cfg.Mappings), len(featuredServers))
	}
	for i, mapping := range cfg.Mappings {
		domain := featuredServers[i]
		if mapping.Domain != domain+"." {
			t.Fatalf("domain = %q, want %q", mapping.Domain, domain+".")
		}
		if want := domain + ":19132"; mapping.Target != want {
			t.Fatalf("target = %q, want %q", mapping.Target, want)
		}
	}
}

func TestLoadOrCreateWritesDefaultThenReusesIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.yaml")

	cfg, created, err := LoadOrCreate(path)
	if err != nil {
		t.Fatalf("LoadOrCreate() error = %v", err)
	}
	if !created {
		t.Fatal("created = false, want true for a missing file")
	}
	if len(cfg.Mappings) != len(featuredServers) {
		t.Fatalf("mappings = %d, want %d", len(cfg.Mappings), len(featuredServers))
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(contents), featuredServers[0]) {
		t.Fatalf("written configuration missing %q: %s", featuredServers[0], contents)
	}

	reloaded, created, err := LoadOrCreate(path)
	if err != nil {
		t.Fatalf("LoadOrCreate() second error = %v", err)
	}
	if created {
		t.Fatal("created = true, want false for an existing file")
	}
	if len(reloaded.Mappings) != len(cfg.Mappings) {
		t.Fatalf("reloaded mappings = %d, want %d", len(reloaded.Mappings), len(cfg.Mappings))
	}
}

func TestLoadOrCreateReturnsErrorForInvalidFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("dns: [\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, _, err := LoadOrCreate(path); err == nil {
		t.Fatal("LoadOrCreate() error = nil, want a decode error")
	}
}

func TestSaveRejectsInvalidConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")

	if err := Save(path, Config{}); err == nil {
		t.Fatal("Save() error = nil, want a validation error")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Stat() error = %v, want the file to be absent", err)
	}
}

func TestSaveReplacesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := Save(path, Default()); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	cfg := Default()
	cfg.Mappings = cfg.Mappings[:1]
	cfg.Mappings[0].Target = "home.example.net:20001"
	if err := Save(path, cfg); err != nil {
		t.Fatalf("Save() second error = %v", err)
	}

	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(reloaded.Mappings) != 1 {
		t.Fatalf("mappings = %d, want 1", len(reloaded.Mappings))
	}
	if reloaded.Mappings[0].Target != "home.example.net:20001" {
		t.Fatalf("target = %q, want %q", reloaded.Mappings[0].Target, "home.example.net:20001")
	}
}
