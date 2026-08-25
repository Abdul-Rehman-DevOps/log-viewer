package archive

import (
	"os"
	"strconv"
	"strings"
	"time"
)

const DefaultRetentionDays = 3

// Config controls S3 log archival (last N days retained for history queries).
type Config struct {
	Enabled        bool
	Bucket         string
	Region         string
	Prefix         string
	Endpoint       string // optional custom endpoint (MinIO, etc.)
	ForcePathStyle bool
	RetentionDays  int
	Interval       time.Duration
	// BackfillSeconds used on first collect for a pod (kubelet window).
	BackfillSeconds int64
}

func ConfigFromEnv() Config {
	enabled := truthy(envOr("LOG_VIEWER_S3_ENABLED", "false"))
	retention := intFromEnv("LOG_VIEWER_S3_RETENTION_DAYS", DefaultRetentionDays)
	if retention < 1 {
		retention = DefaultRetentionDays
	}
	intervalSec := intFromEnv("LOG_VIEWER_S3_INTERVAL_SEC", 120)
	if intervalSec < 30 {
		intervalSec = 30
	}
	backfill := int64(intFromEnv("LOG_VIEWER_S3_BACKFILL_SEC", 3600))
	if backfill < 0 {
		backfill = 0
	}
	prefix := strings.Trim(envOr("LOG_VIEWER_S3_PREFIX", "log-viewer"), "/")
	return Config{
		Enabled:         enabled,
		Bucket:          strings.TrimSpace(os.Getenv("LOG_VIEWER_S3_BUCKET")),
		Region:          envOr("LOG_VIEWER_S3_REGION", "us-east-1"),
		Prefix:          prefix,
		Endpoint:        strings.TrimSpace(os.Getenv("LOG_VIEWER_S3_ENDPOINT")),
		ForcePathStyle:  truthy(envOr("LOG_VIEWER_S3_FORCE_PATH_STYLE", "false")),
		RetentionDays:   retention,
		Interval:        time.Duration(intervalSec) * time.Second,
		BackfillSeconds: backfill,
	}
}

func (c Config) Valid() bool {
	return c.Enabled && c.Bucket != ""
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func intFromEnv(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
