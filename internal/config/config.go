package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPAddr                 string
	DatabaseURL              string
	// MigrateDatabaseURL is an optional DDL-capable role used only for migrations (least privilege).
	MigrateDatabaseURL       string
	DatabaseMaxConns         int
	DatabaseMinConns         int
	LogLevel                 string
	LogFormat                string
	MetricsAddr              string
	DefaultMaxAttempts       int
	DefaultJobTimeoutSeconds int
	MaxJobTimeoutSeconds     int
	MaxScheduleHorizon       time.Duration
	IdempotencyTTL           time.Duration
	TerminalJobRetention     time.Duration
	AllowedQueues            []string
	AllowedOrigins           []string
	RetryBaseDelay           time.Duration
	RetryMaxDelay            time.Duration
	DefaultLeaseDuration     time.Duration
	LeaseRenewInterval       time.Duration
	WorkerHeartbeatInterval  time.Duration
	ClaimPollInterval        time.Duration
	WorkerConcurrency        int           // max jobs one worker process executes at once
	ClaimBatchSize           int           // max jobs leased per claim transaction
	CommitRetryBudget        time.Duration // how long a worker keeps retrying to record a finished job
	TrustForwardedFor        bool          // honour X-Forwarded-For (ONLY when the API is reachable solely via your reverse proxy)
	RecoveryInterval         time.Duration
	SchedulerInterval        time.Duration
	ScheduleMisfireGrace     time.Duration
	RetentionInterval        time.Duration
	MaxRequestBytes          int64
	MaxPayloadBytes          int64
	ClientRatePerSecond      int
	ClientRateBurst          int
	BatchCompletion          bool
	BatchCompletionMax       int
	ShutdownTimeout          time.Duration
}

func getEnv(key, defaultVal string) string {
	val, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(val) == "" {
		return defaultVal
	}
	return strings.TrimSpace(val)
}

func getEnvBool(key string, defaultVal bool) bool {
	switch strings.ToLower(os.Getenv(key)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return defaultVal
}

func getEnvInt(key string, defaultVal int) int {
	valStr := getEnv(key, "")
	if valStr == "" {
		return defaultVal
	}
	val, err := strconv.Atoi(valStr)
	if err != nil {
		return defaultVal
	}
	return val
}

func getEnvDuration(key string, defaultVal time.Duration) time.Duration {
	valStr := getEnv(key, "")
	if valStr == "" {
		return defaultVal
	}
	d, err := time.ParseDuration(valStr)
	if err != nil {
		// support days like "365d" or "30d"
		if strings.HasSuffix(valStr, "d") {
			daysStr := strings.TrimSuffix(valStr, "d")
			days, errDays := strconv.Atoi(daysStr)
			if errDays == nil {
				return time.Duration(days) * 24 * time.Hour
			}
		}
		return defaultVal
	}
	return d
}

func Load() (*Config, error) {
	allowedQueuesRaw := getEnv("QF_ALLOWED_QUEUES", "default,notifications,background,maintenance")
	var allowedQueues []string
	for _, q := range strings.Split(allowedQueuesRaw, ",") {
		q = strings.TrimSpace(q)
		if q != "" {
			allowedQueues = append(allowedQueues, q)
		}
	}

	allowedOriginsRaw := getEnv("QF_ALLOWED_ORIGINS", "*")
	var allowedOrigins []string
	for _, o := range strings.Split(allowedOriginsRaw, ",") {
		o = strings.TrimSpace(o)
		if o != "" {
			allowedOrigins = append(allowedOrigins, o)
		}
	}

	cfg := &Config{
		HTTPAddr:                 getEnv("QF_HTTP_ADDR", "0.0.0.0:8080"),
		DatabaseURL:              getEnv("QF_DATABASE_URL", "postgres://postgres:postgres@localhost:5435/queueforge?sslmode=disable"),
		MigrateDatabaseURL:       getEnv("QF_MIGRATE_DATABASE_URL", ""),
		DatabaseMaxConns:         getEnvInt("QF_DATABASE_MAX_CONNS", 20),
		DatabaseMinConns:         getEnvInt("QF_DATABASE_MIN_CONNS", 2),
		LogLevel:                 getEnv("QF_LOG_LEVEL", "info"),
		LogFormat:                getEnv("QF_LOG_FORMAT", "json"),
		MetricsAddr:              getEnv("QF_METRICS_ADDR", "127.0.0.1:9090"),
		DefaultMaxAttempts:       getEnvInt("QF_DEFAULT_MAX_ATTEMPTS", 5),
		DefaultJobTimeoutSeconds: getEnvInt("QF_DEFAULT_JOB_TIMEOUT_SECONDS", 300),
		MaxJobTimeoutSeconds:     getEnvInt("QF_MAX_JOB_TIMEOUT_SECONDS", 3600),
		MaxScheduleHorizon:       getEnvDuration("QF_MAX_SCHEDULE_HORIZON", 365*24*time.Hour),
		IdempotencyTTL:           getEnvDuration("QF_IDEMPOTENCY_TTL", 24*time.Hour),
		TerminalJobRetention:     getEnvDuration("QF_TERMINAL_JOB_RETENTION", 720*time.Hour), // 30 days
		AllowedQueues:            allowedQueues,
		AllowedOrigins:           allowedOrigins,
		RetryBaseDelay:           getEnvDuration("QF_RETRY_BASE_DELAY", 1*time.Second),
		RetryMaxDelay:            getEnvDuration("QF_RETRY_MAX_DELAY", 15*time.Minute),
		DefaultLeaseDuration:     getEnvDuration("QF_DEFAULT_LEASE_DURATION", 60*time.Second),
		LeaseRenewInterval:       getEnvDuration("QF_LEASE_RENEW_INTERVAL", 20*time.Second),
		WorkerHeartbeatInterval:  getEnvDuration("QF_WORKER_HEARTBEAT_INTERVAL", 10*time.Second),
		ClaimPollInterval:        getEnvDuration("QF_CLAIM_POLL_INTERVAL", 250*time.Millisecond),
		WorkerConcurrency:        getEnvInt("QF_WORKER_CONCURRENCY", 16),
		ClaimBatchSize:           getEnvInt("QF_CLAIM_BATCH_SIZE", 8),
		CommitRetryBudget:        getEnvDuration("QF_COMMIT_RETRY_BUDGET", 60*time.Second),
		TrustForwardedFor:        getEnvBool("QF_TRUST_FORWARDED_FOR", false),
		RecoveryInterval:         getEnvDuration("QF_RECOVERY_INTERVAL", 5*time.Second),
		SchedulerInterval:        getEnvDuration("QF_SCHEDULER_INTERVAL", 1*time.Second),
		ScheduleMisfireGrace:     getEnvDuration("QF_SCHEDULE_MISFIRE_GRACE", 30*time.Second),
		RetentionInterval:        getEnvDuration("QF_RETENTION_INTERVAL", 10*time.Minute),
		MaxRequestBytes:          int64(getEnvInt("QF_MAX_REQUEST_BYTES", 1048576)), // 1 MB
		ClientRatePerSecond:      getEnvInt("QF_CLIENT_RATE_PER_SECOND", 0),
		ClientRateBurst:          getEnvInt("QF_CLIENT_RATE_BURST", 400),
		BatchCompletion:          getEnvBool("QF_BATCH_COMPLETION", false),
		BatchCompletionMax:       getEnvInt("QF_BATCH_COMPLETION_MAX", 64),
		MaxPayloadBytes:          int64(getEnvInt("QF_MAX_PAYLOAD_BYTES", 262144)),  // 256 KB
		ShutdownTimeout:          getEnvDuration("QF_SHUTDOWN_TIMEOUT", 30*time.Second),
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (c *Config) Validate() error {
	if c.DatabaseURL == "" {
		return fmt.Errorf("database URL cannot be empty")
	}
	if _, err := url.Parse(c.DatabaseURL); err != nil {
		return fmt.Errorf("invalid database URL: %w", err)
	}
	if c.DatabaseMaxConns < 1 {
		return fmt.Errorf("database max connections must be at least 1")
	}
	if c.DatabaseMinConns < 0 || c.DatabaseMinConns > c.DatabaseMaxConns {
		return fmt.Errorf("invalid database min connections (%d), must be between 0 and max conns (%d)", c.DatabaseMinConns, c.DatabaseMaxConns)
	}
	if c.DefaultMaxAttempts < 1 || c.DefaultMaxAttempts > 10 {
		return fmt.Errorf("default max attempts must be between 1 and 10")
	}
	if c.DefaultJobTimeoutSeconds < 1 || c.DefaultJobTimeoutSeconds > c.MaxJobTimeoutSeconds {
		return fmt.Errorf("default job timeout must be between 1 and max job timeout (%d)", c.MaxJobTimeoutSeconds)
	}
	if c.LeaseRenewInterval >= c.DefaultLeaseDuration {
		return fmt.Errorf("lease renewal interval (%v) must be substantially less than lease duration (%v)", c.LeaseRenewInterval, c.DefaultLeaseDuration)
	}
	if c.RetryBaseDelay > c.RetryMaxDelay {
		return fmt.Errorf("retry base delay (%v) cannot be greater than retry max delay (%v)", c.RetryBaseDelay, c.RetryMaxDelay)
	}
	if c.TerminalJobRetention < c.IdempotencyTTL {
		return fmt.Errorf("terminal job retention (%v) must be >= idempotency key TTL (%v)", c.TerminalJobRetention, c.IdempotencyTTL)
	}
	if c.MaxPayloadBytes >= c.MaxRequestBytes {
		return fmt.Errorf("max payload bytes (%d) must be strictly less than max request bytes (%d)", c.MaxPayloadBytes, c.MaxRequestBytes)
	}
	if len(c.AllowedQueues) == 0 {
		return fmt.Errorf("allowed queues allowlist cannot be empty")
	}
	return nil
}

func (c *Config) IsQueueAllowed(queue string) bool {
	for _, q := range c.AllowedQueues {
		if q == queue {
			return true
		}
	}
	return false
}

// MigrationURL is the connection string used to apply schema migrations. Set
// QF_MIGRATE_DATABASE_URL to a separate role that owns the schema so the long-running API and
// workers can connect with a role that cannot run DDL (see deploy/sql/least_privilege.sql).
func (c *Config) MigrationURL() string {
	if c.MigrateDatabaseURL != "" {
		return c.MigrateDatabaseURL
	}
	return c.DatabaseURL
}
