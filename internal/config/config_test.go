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
