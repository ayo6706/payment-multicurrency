package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store provides access to generated queries and transaction scoping.
type Store struct {
	db      *pgxpool.Pool
	queries *Queries
}

// NewStore creates a store wrapper around a pgx connection pool.
func NewStore(db *pgxpool.Pool) *Store {
	return &Store{
		db:      db,
		queries: New(db),
	}
}

// Queries returns the non-transactional query set.
func (s *Store) Queries() *Queries {
	return s.queries
}

// RunInTx executes fn within a database transaction.
func (s *Store) RunInTx(ctx context.Context, fn func(q *Queries) error) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := fn(s.queries.WithTx(tx)); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}
