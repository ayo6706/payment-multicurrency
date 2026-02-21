package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

// Config holds all runtime configuration derived from environment variables.
type Config struct {
	HTTPPort               string
	DatabaseURL            string
	RedisURL               string
	JWTSecret              string
	JWTIssuer              string
	JWTAudience            string
	WebhookHMACKey         string
	WebhookSkipSignature   bool
	PayoutPollInterval     time.Duration
	PayoutBatchSize        int32
	ReconciliationInterval time.Duration
	PublicRateLimitRPS     int
	AuthRateLimitRPS       int
	LogLevel               string
	IdempotencyTTL         time.Duration
}

// Load reads environment variables using viper and returns a typed config.
func Load() (*Config, error) {
	_ = godotenv.Load()

	v := viper.New()
	v.AutomaticEnv()
	bindEnv(v, "port", "PORT", "PAYMENT_PORT")
	bindEnv(v, "database_url", "DATABASE_URL", "PAYMENT_DATABASE_URL")
	bindEnv(v, "redis_url", "REDIS_URL", "PAYMENT_REDIS_URL")
	bindEnv(v, "jwt_secret", "JWT_SECRET", "PAYMENT_JWT_SECRET")
	bindEnv(v, "jwt_issuer", "JWT_ISSUER", "PAYMENT_JWT_ISSUER")
	bindEnv(v, "jwt_audience", "JWT_AUDIENCE", "PAYMENT_JWT_AUDIENCE")
	bindEnv(v, "webhook_hmac_key", "WEBHOOK_HMAC_KEY", "PAYMENT_WEBHOOK_HMAC_KEY")
	bindEnv(v, "webhook_skip_sig", "WEBHOOK_SKIP_SIG", "PAYMENT_WEBHOOK_SKIP_SIG")
	bindEnv(v, "payout_poll_interval", "PAYOUT_POLL_INTERVAL", "PAYMENT_PAYOUT_POLL_INTERVAL")
	bindEnv(v, "payout_batch_size", "PAYOUT_BATCH_SIZE", "PAYMENT_PAYOUT_BATCH_SIZE")
	bindEnv(v, "reconciliation_interval", "RECONCILIATION_INTERVAL", "PAYMENT_RECONCILIATION_INTERVAL")
	bindEnv(v, "public_rate_limit_rps", "PUBLIC_RATE_LIMIT_RPS", "PAYMENT_PUBLIC_RATE_LIMIT_RPS")
	bindEnv(v, "auth_rate_limit_rps", "AUTH_RATE_LIMIT_RPS", "PAYMENT_AUTH_RATE_LIMIT_RPS")
	bindEnv(v, "log_level", "LOG_LEVEL", "PAYMENT_LOG_LEVEL")
	bindEnv(v, "idempotency_ttl", "IDEMPOTENCY_TTL", "PAYMENT_IDEMPOTENCY_TTL")

	v.SetDefault("port", "8080")
	v.SetDefault("database_url", "postgres://user:password@localhost:5432/payment_system?sslmode=disable")
	v.SetDefault("redis_url", "redis://localhost:6379/0")
	v.SetDefault("jwt_secret", "")
	v.SetDefault("jwt_issuer", "payment-multicurrency")
	v.SetDefault("jwt_audience", "payment-api")
	v.SetDefault("webhook_hmac_key", "")
	v.SetDefault("webhook_skip_sig", false)
	v.SetDefault("payout_poll_interval", "10s")
	v.SetDefault("payout_batch_size", 10)
	v.SetDefault("reconciliation_interval", "24h")
	v.SetDefault("public_rate_limit_rps", 10)
	v.SetDefault("auth_rate_limit_rps", 100)
	v.SetDefault("log_level", "info")
	v.SetDefault("idempotency_ttl", "24h")

	pollInterval, err := time.ParseDuration(v.GetString("payout_poll_interval"))
	if err != nil {
		return nil, fmt.Errorf("invalid PAYOUT_POLL_INTERVAL: %w", err)
	}

	ttl, err := time.ParseDuration(v.GetString("idempotency_ttl"))
	if err != nil {
		return nil, fmt.Errorf("invalid IDEMPOTENCY_TTL: %w", err)
	}
	reconciliationInterval, err := time.ParseDuration(v.GetString("reconciliation_interval"))
	if err != nil {
		return nil, fmt.Errorf("invalid RECONCILIATION_INTERVAL: %w", err)
	}

	batchSize := v.GetInt("payout_batch_size")
	if batchSize <= 0 {
		batchSize = 10
	}

	cfg := &Config{
		HTTPPort:               v.GetString("port"),
		DatabaseURL:            v.GetString("database_url"),
		RedisURL:               v.GetString("redis_url"),
		JWTSecret:              v.GetString("jwt_secret"),
		JWTIssuer:              v.GetString("jwt_issuer"),
		JWTAudience:            v.GetString("jwt_audience"),
		WebhookHMACKey:         v.GetString("webhook_hmac_key"),
		WebhookSkipSignature:   v.GetBool("webhook_skip_sig"),
		PayoutPollInterval:     pollInterval,
		PayoutBatchSize:        int32(batchSize),
		ReconciliationInterval: reconciliationInterval,
		PublicRateLimitRPS:     max(v.GetInt("public_rate_limit_rps"), 1),
		AuthRateLimitRPS:       max(v.GetInt("auth_rate_limit_rps"), 1),
		LogLevel:               v.GetString("log_level"),
		IdempotencyTTL:         ttl,
	}

	if strings.TrimSpace(cfg.JWTSecret) == "" {
		return nil, fmt.Errorf("JWT_SECRET is required")
	}
	if len(cfg.JWTSecret) < 32 {
		return nil, fmt.Errorf("JWT_SECRET must be at least 32 characters")
	}
	if !cfg.WebhookSkipSignature && strings.TrimSpace(cfg.WebhookHMACKey) == "" {
		return nil, fmt.Errorf("WEBHOOK_HMAC_KEY is required when WEBHOOK_SKIP_SIG is false")
	}
	if strings.TrimSpace(cfg.JWTIssuer) == "" {
		return nil, fmt.Errorf("JWT_ISSUER is required")
	}
	if strings.TrimSpace(cfg.JWTAudience) == "" {
		return nil, fmt.Errorf("JWT_AUDIENCE is required")
	}

	return cfg, nil
}

func bindEnv(v *viper.Viper, key string, names ...string) {
	args := append([]string{key}, names...)
	_ = v.BindEnv(args...)
}
