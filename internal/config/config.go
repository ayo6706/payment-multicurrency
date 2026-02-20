package config

import (
	"fmt"
	"time"

	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

// Config holds all runtime configuration derived from environment variables.
type Config struct {
	HTTPPort             string
	DatabaseURL          string
	RedisURL             string
	JWTSecret            string
	WebhookHMACKey       string
	WebhookSkipSignature bool
	PayoutPollInterval   time.Duration
	PayoutBatchSize      int32
	PublicRateLimitRPS   int
	AuthRateLimitRPS     int
	LogLevel             string
	IdempotencyTTL       time.Duration
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
	bindEnv(v, "webhook_hmac_key", "WEBHOOK_HMAC_KEY", "PAYMENT_WEBHOOK_HMAC_KEY")
	bindEnv(v, "webhook_skip_sig", "WEBHOOK_SKIP_SIG", "PAYMENT_WEBHOOK_SKIP_SIG")
	bindEnv(v, "payout_poll_interval", "PAYOUT_POLL_INTERVAL", "PAYMENT_PAYOUT_POLL_INTERVAL")
	bindEnv(v, "payout_batch_size", "PAYOUT_BATCH_SIZE", "PAYMENT_PAYOUT_BATCH_SIZE")
	bindEnv(v, "public_rate_limit_rps", "PUBLIC_RATE_LIMIT_RPS", "PAYMENT_PUBLIC_RATE_LIMIT_RPS")
	bindEnv(v, "auth_rate_limit_rps", "AUTH_RATE_LIMIT_RPS", "PAYMENT_AUTH_RATE_LIMIT_RPS")
	bindEnv(v, "log_level", "LOG_LEVEL", "PAYMENT_LOG_LEVEL")
	bindEnv(v, "idempotency_ttl", "IDEMPOTENCY_TTL", "PAYMENT_IDEMPOTENCY_TTL")

	v.SetDefault("port", "8080")
	v.SetDefault("database_url", "postgres://user:password@localhost:5432/payment_system?sslmode=disable")
	v.SetDefault("redis_url", "redis://localhost:6379/0")
	v.SetDefault("jwt_secret", "dev-secret-change-me")
	v.SetDefault("webhook_hmac_key", "dev-key-change-in-production")
	v.SetDefault("webhook_skip_sig", false)
	v.SetDefault("payout_poll_interval", "10s")
	v.SetDefault("payout_batch_size", 10)
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

	batchSize := v.GetInt("payout_batch_size")
	if batchSize <= 0 {
		batchSize = 10
	}

	cfg := &Config{
		HTTPPort:             v.GetString("port"),
		DatabaseURL:          v.GetString("database_url"),
		RedisURL:             v.GetString("redis_url"),
		JWTSecret:            v.GetString("jwt_secret"),
		WebhookHMACKey:       v.GetString("webhook_hmac_key"),
		WebhookSkipSignature: v.GetBool("webhook_skip_sig"),
		PayoutPollInterval:   pollInterval,
		PayoutBatchSize:      int32(batchSize),
		PublicRateLimitRPS:   max(v.GetInt("public_rate_limit_rps"), 1),
		AuthRateLimitRPS:     max(v.GetInt("auth_rate_limit_rps"), 1),
		LogLevel:             v.GetString("log_level"),
		IdempotencyTTL:       ttl,
	}

	return cfg, nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func bindEnv(v *viper.Viper, key string, names ...string) {
	args := append([]string{key}, names...)
	_ = v.BindEnv(args...)
}
