package service

import (
	"context"

	"github.com/ayo6706/payment-multicurrency/internal/repository"
)

// QueryStore defines the minimal data access contract required by services.
type QueryStore interface {
	Queries() *repository.Queries
	RunInTx(ctx context.Context, fn func(q *repository.Queries) error) error
}
