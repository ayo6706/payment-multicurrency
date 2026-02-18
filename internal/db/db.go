package db

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func Connect() (*pgxpool.Pool, error) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		// Fallback for local development if not set
		dbURL = "postgres://user:password@localhost:5432/payment_system?sslmode=disable"
	}

	config, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		return nil, fmt.Errorf("unable to parse database config: %w", err)
	}

	// Set connection pool settings
	config.MaxConns = 10
	config.MinConns = 2
	config.MaxConnLifetime = time.Hour
	config.MaxConnIdleTime = 30 * time.Minute

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("unable to create connection pool: %w", err)
	}

	// Ping to verify connection
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("unable to connect to database: %w", err)
	}

	return pool, nil
}
