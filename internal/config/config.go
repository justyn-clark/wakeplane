package config

import (
	"flag"
	"os"
	"strconv"
	"time"
)

type Config struct {
	HTTPAddress        string
	StoreDialect       string
	DatabasePath       string
	DatabaseURL        string
	SchedulerInterval  time.Duration
	DispatcherInterval time.Duration
	LeaseTTL           time.Duration
	WorkerID           string
	Version            string
	ReceiptMaxBytes    int
	RunRetentionDays   int
	AuthToken          string
	RequestAudit       bool
}

func FromEnv(version string) Config {
	cfg := Config{
		HTTPAddress:        envOrDefault("WAKEPLANE_HTTP_ADDR", ":8080"),
		StoreDialect:       envOrDefault("WAKEPLANE_STORE", "sqlite"),
		DatabasePath:       envOrDefault("WAKEPLANE_DB_PATH", "./wakeplane.db"),
		DatabaseURL:        os.Getenv("WAKEPLANE_DATABASE_URL"),
		SchedulerInterval:  durationEnv("WAKEPLANE_SCHEDULER_INTERVAL_SECONDS", 5),
		DispatcherInterval: durationEnv("WAKEPLANE_DISPATCHER_INTERVAL_SECONDS", 2),
		LeaseTTL:           durationEnv("WAKEPLANE_LEASE_TTL_SECONDS", 30),
		WorkerID:           envOrDefault("WAKEPLANE_WORKER_ID", "wrk_local"),
		Version:            version,
		ReceiptMaxBytes:    intEnv("WAKEPLANE_RECEIPT_MAX_BYTES", 262144),
		RunRetentionDays:   intEnv("WAKEPLANE_RUN_RETENTION_DAYS", 0),
		AuthToken:          os.Getenv("WAKEPLANE_AUTH_TOKEN"),
		RequestAudit:       boolEnv("WAKEPLANE_REQUEST_AUDIT", true),
	}
	return cfg
}

func (c Config) WithDefaults() Config {
	if c.HTTPAddress == "" {
		c.HTTPAddress = ":8080"
	}
	if c.DatabasePath == "" {
		c.DatabasePath = "./wakeplane.db"
	}
	if c.StoreDialect == "" {
		c.StoreDialect = "sqlite"
	}
	if c.SchedulerInterval == 0 {
		c.SchedulerInterval = 5 * time.Second
	}
	if c.DispatcherInterval == 0 {
		c.DispatcherInterval = 2 * time.Second
	}
	if c.LeaseTTL == 0 {
		c.LeaseTTL = 30 * time.Second
	}
	if c.WorkerID == "" {
		c.WorkerID = "wrk_local"
	}
	if c.ReceiptMaxBytes == 0 {
		c.ReceiptMaxBytes = 262144
	}
	return c
}

func (c *Config) BindFlags(fs *flag.FlagSet) {
	fs.StringVar(&c.HTTPAddress, "http", c.HTTPAddress, "HTTP listen address")
	fs.StringVar(&c.DatabasePath, "db", c.DatabasePath, "SQLite database path")
	fs.StringVar(&c.StoreDialect, "store", c.StoreDialect, "storage backend: sqlite or postgres")
	fs.StringVar(&c.DatabaseURL, "database-url", c.DatabaseURL, "Postgres database URL")
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func durationEnv(key string, fallback int) time.Duration {
	return time.Duration(intEnv(key, fallback)) * time.Second
}

func intEnv(key string, fallback int) int {
	if raw := os.Getenv(key); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			return n
		}
	}
	return fallback
}

func boolEnv(key string, fallback bool) bool {
	if raw := os.Getenv(key); raw != "" {
		switch raw {
		case "1", "true", "TRUE", "yes", "YES", "on", "ON":
			return true
		case "0", "false", "FALSE", "no", "NO", "off", "OFF":
			return false
		}
	}
	return fallback
}
