package config

import "testing"

func TestFromEnvLoadsStoreDialectAndDatabaseURL(t *testing.T) {
	t.Setenv("WAKEPLANE_STORE", "postgres")
	t.Setenv("WAKEPLANE_DATABASE_URL", "postgres://wakeplane:secret@localhost:5432/wakeplane")

	cfg := FromEnv("test")

	if cfg.StoreDialect != "postgres" {
		t.Fatalf("expected postgres store dialect, got %q", cfg.StoreDialect)
	}
	if cfg.DatabaseURL != "postgres://wakeplane:secret@localhost:5432/wakeplane" {
		t.Fatalf("expected database URL from env, got %q", cfg.DatabaseURL)
	}
}

func TestWithDefaultsKeepsSQLiteAsDefaultStore(t *testing.T) {
	cfg := Config{}.WithDefaults()

	if cfg.StoreDialect != "sqlite" {
		t.Fatalf("expected sqlite default store, got %q", cfg.StoreDialect)
	}
	if cfg.DatabasePath != "./wakeplane.db" {
		t.Fatalf("expected default sqlite database path, got %q", cfg.DatabasePath)
	}
}

func TestPlatformPortAndExplicitListenAddress(t *testing.T) {
	t.Setenv("PORT", "9123")
	t.Setenv("WAKEPLANE_HTTP_ADDR", "")
	if got := FromEnv("test").HTTPAddress; got != ":9123" {
		t.Fatalf("platform listen address=%q", got)
	}
	t.Setenv("WAKEPLANE_HTTP_ADDR", "127.0.0.1:8123")
	if got := FromEnv("test").HTTPAddress; got != "127.0.0.1:8123" {
		t.Fatalf("explicit listen address=%q", got)
	}
}
