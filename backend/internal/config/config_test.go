package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadFromEnvironment(t *testing.T) {
	t.Setenv("CONFIG_PATH", "")
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("HTTP_ADDR", ":9000")
	t.Setenv("MODEL_PATH", "model/asr-model")
	t.Setenv("REDIS_URL", "redis://localhost:6380/0")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.HTTPServer.Address != ":9000" || cfg.Database.URL != "postgres://example" {
		t.Fatalf("unexpected environment config: %+v", cfg)
	}
	if cfg.Database.ConnectTimeout != 5*time.Second || cfg.ShutdownTimeout != 10*time.Second {
		t.Fatalf("defaults were not applied: %+v", cfg)
	}
	if cfg.Google.Enabled() || cfg.Google.EventDuration != time.Hour {
		t.Fatalf("unexpected Google defaults: %+v", cfg.Google)
	}
}

func TestLoadGoogleFromEnvironment(t *testing.T) {
	t.Setenv("CONFIG_PATH", "")
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("MODEL_PATH", "model/asr-model")
	t.Setenv("REDIS_URL", "redis://localhost:6380/0")
	t.Setenv("GOOGLE_CLIENT_ID", "client-id")
	t.Setenv("GOOGLE_CLIENT_SECRET", "client-secret")
	t.Setenv("GOOGLE_EVENT_DURATION", "30m")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if !cfg.Google.Enabled() || cfg.Google.EventDuration != 30*time.Minute {
		t.Fatalf("unexpected Google config: %+v", cfg.Google)
	}
}

func TestLoadEnvironmentOverridesYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("log_level: debug\nhttp-server:\n  address: ':8000'\n  read_timeout: 15s\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONFIG_PATH", path)
	t.Setenv("LOG_LEVEL", "warn")
	t.Setenv("HTTP_ADDR", ":7000")
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("MODEL_PATH", "model/asr-model")
	t.Setenv("REDIS_URL", "redis://localhost:6380/0")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.LogLevel != "warn" || cfg.HTTPServer.Address != ":7000" || cfg.HTTPServer.ReadTimeout != 15*time.Second {
		t.Fatalf("unexpected YAML and environment merge: %+v", cfg)
	}
}
