package idempotency

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ayo6706/payment-multicurrency/internal/repository"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

var (
	ErrNotFound     = errors.New("idempotency key not found")
	ErrHashMismatch = errors.New("idempotency key body mismatch")
	ErrInProgress   = errors.New("idempotency key in progress")
)

const redisKeyPrefix = "idempotency"

type Record struct {
	Key         string
	RequestHash string
	Status      int
	Body        []byte
	ContentType string
	ServedBy    string
}

type Store struct {
	redis redis.Cmdable
	db    *pgxpool.Pool
	ttl   time.Duration
}

func NewStore(redis redis.Cmdable, db *pgxpool.Pool, ttl time.Duration) *Store {
	return &Store{redis: redis, db: db, ttl: ttl}
}

type cacheEnvelope struct {
	Key         string `json:"key"`
	Hash        string `json:"hash"`
	Status      int    `json:"status"`
	Body        []byte `json:"body"`
	ContentType string `json:"content_type"`
}

func (s *Store) Lookup(ctx context.Context, key, requestHash string) (*Record, error) {
	if s.redis != nil {
		val, err := s.redis.Get(ctx, redisKey(key)).Result()
		if err == nil {
			var env cacheEnvelope
			if json.Unmarshal([]byte(val), &env) == nil {
				if env.Hash != requestHash {
					return nil, ErrHashMismatch
				}
				return &Record{
					Key:         env.Key,
					RequestHash: env.Hash,
					Status:      env.Status,
					Body:        env.Body,
					ContentType: env.ContentType,
					ServedBy:    "redis",
				}, nil
			}
		} else if err != redis.Nil {
			zap.L().Warn("redis idempotency lookup failed", zap.Error(err))
		}
	}

	queries := repository.New(s.db)
	row, err := queries.GetIdempotencyKey(ctx, key)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("lookup idempotency key: %w", err)
	}

	rec := Record{
		Key:         row.IdempotencyKey,
		RequestHash: row.RequestHash,
		Status:      int(row.ResponseStatus),
		Body:        row.ResponseBody,
		ContentType: row.ContentType,
	}

	if rec.RequestHash != requestHash {
		return nil, ErrHashMismatch
	}
	if row.InProgress {
		return nil, ErrInProgress
	}
	rec.ServedBy = "postgres"
	s.cache(ctx, rec)
	return &rec, nil
}

func (s *Store) Reserve(ctx context.Context, key, requestHash, method, path string) (bool, error) {
	queries := repository.New(s.db)
	_, err := queries.ReserveIdempotencyKey(ctx, repository.ReserveIdempotencyKeyParams{
		IdempotencyKey: key,
		RequestHash:    requestHash,
		Method:         method,
		Path:           path,
	})
	if err == nil {
		return true, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return false, fmt.Errorf("reserve idempotency key: %w", err)
}

func (s *Store) Finalize(ctx context.Context, key, requestHash string, status int, body []byte, contentType string) (*Record, error) {
	queries := repository.New(s.db)
	row, err := queries.FinalizeIdempotencyKey(ctx, repository.FinalizeIdempotencyKeyParams{
		ResponseStatus: int32(status),
		ResponseBody:   body,
		ContentType:    contentType,
		IdempotencyKey: key,
		RequestHash:    requestHash,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("finalize idempotency key: %w", err)
	}

	rec := &Record{
		Key:         row.IdempotencyKey,
		RequestHash: row.RequestHash,
		Status:      int(row.ResponseStatus),
		Body:        row.ResponseBody,
		ContentType: row.ContentType,
		ServedBy:    "postgres",
	}
	s.cache(ctx, *rec)
	return rec, nil
}

func (s *Store) WaitForCompletion(ctx context.Context, key, requestHash string) (*Record, error) {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		rec, err := s.Lookup(ctx, key, requestHash)
		if err == nil {
			return rec, nil
		}
		if errors.Is(err, ErrInProgress) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-ticker.C:
				continue
			}
		}
		return nil, err
	}
}

func (s *Store) cache(ctx context.Context, rec Record) {
	if s.redis == nil {
		return
	}
	env := cacheEnvelope{
		Key:         rec.Key,
		Hash:        rec.RequestHash,
		Status:      rec.Status,
		Body:        rec.Body,
		ContentType: rec.ContentType,
	}
	payload, err := json.Marshal(env)
	if err != nil {
		zap.L().Warn("marshal idempotency cache", zap.Error(err))
		return
	}
	if err := s.redis.Set(ctx, redisKey(rec.Key), payload, s.ttl).Err(); err != nil {
		zap.L().Warn("redis idempotency cache set failed", zap.Error(err))
	}
}

func redisKey(key string) string {
	return fmt.Sprintf("%s:%s", redisKeyPrefix, key)
}
