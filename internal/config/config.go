package config

import (
	"flag"
	"os"
	"strconv"
	"time"
)

type Config struct {
	HTTPAddress        string
	DatabasePath       string
	SchedulerInterval  time.Duration
	DispatcherInterval time.Duration
	LeaseTTL           time.Duration
	WorkerID           string
	Version            string
}

func FromEnv(version string) Config {
	cfg := Config{
		HTTPAddress:        envOrDefault("TIMEKEEPER_HTTP_ADDR", ":8080"),
		DatabasePath:       envOrDefault("TIMEKEEPER_DB_PATH", "./timekeeper.db"),
		SchedulerInterval:  durationEnv("TIMEKEEPER_SCHEDULER_INTERVAL_SECONDS", 5),
		DispatcherInterval: durationEnv("TIMEKEEPER_DISPATCHER_INTERVAL_SECONDS", 2),
		LeaseTTL:           durationEnv("TIMEKEEPER_LEASE_TTL_SECONDS", 30),
		WorkerID:           envOrDefault("TIMEKEEPER_WORKER_ID", "wrk_local"),
		Version:            version,
	}
	return cfg
}

func (c *Config) BindFlags(fs *flag.FlagSet) {
	fs.StringVar(&c.HTTPAddress, "http", c.HTTPAddress, "HTTP listen address")
	fs.StringVar(&c.DatabasePath, "db", c.DatabasePath, "SQLite database path")
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func durationEnv(key string, fallback int) time.Duration {
	if raw := os.Getenv(key); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			return time.Duration(n) * time.Second
		}
	}
	return time.Duration(fallback) * time.Second
}
