package repository

import (
	"context"
	"fmt"

	"github.com/ayo6706/payment-multicurrency/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	db      *pgxpool.Pool
	queries *Queries
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{
		db:      db,
		queries: New(db),
	}
}

// Helper to convert google/uuid to pgtype.UUID
func ToPgUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

// Helper to convert pgtype.UUID back to google/uuid
func FromPgUUID(id pgtype.UUID) uuid.UUID {
	return uuid.UUID(id.Bytes)
}

func (r *Repository) CreateUser(ctx context.Context, user *models.User) error {
	if user.Role == "" {
		user.Role = "user"
	}
	createdAt, err := r.queries.CreateUser(ctx, CreateUserParams{
		ID:       ToPgUUID(user.ID),
		Username: user.Username,
		Email:    user.Email,
		Role:     user.Role,
	})
	if err != nil {
		return fmt.Errorf("failed to create user: %w", err)
	}
	user.CreatedAt = createdAt.Time
	return nil
}

func (r *Repository) CreateAccount(ctx context.Context, account *models.Account) error {
	createdAt, err := r.queries.CreateAccount(ctx, CreateAccountParams{
		ID:       ToPgUUID(account.ID),
		UserID:   ToPgUUID(account.UserID),
		Currency: account.Currency,
		Balance:  account.Balance,
	})
	if err != nil {
		return fmt.Errorf("failed to create account: %w", err)
	}
	account.CreatedAt = createdAt.Time
	return nil
}

func (r *Repository) GetUser(ctx context.Context, id uuid.UUID) (*models.User, error) {
	row, err := r.queries.GetUser(ctx, ToPgUUID(id))
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	return &models.User{
		ID:        FromPgUUID(row.ID),
		Username:  row.Username,
		Email:     row.Email,
		Role:      row.Role,
		CreatedAt: row.CreatedAt.Time,
	}, nil
}

func (r *Repository) GetAccount(ctx context.Context, id uuid.UUID) (*models.Account, error) {
	row, err := r.queries.GetAccount(ctx, ToPgUUID(id))
	if err != nil {
		return nil, fmt.Errorf("failed to get account: %w", err)
	}

	return &models.Account{
		ID:        FromPgUUID(row.ID),
		UserID:    FromPgUUID(row.UserID),
		Currency:  row.Currency,
		Balance:   row.Balance,
		CreatedAt: row.CreatedAt.Time,
	}, nil
}

func (r *Repository) GetEntries(ctx context.Context, accountID uuid.UUID, limit, offset int) ([]models.Entry, error) {
	rows, err := r.queries.GetEntries(ctx, GetEntriesParams{
		AccountID: ToPgUUID(accountID),
		Limit:     int32(limit),
		Offset:    int32(offset),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get entries: %w", err)
	}

	entries := make([]models.Entry, len(rows))
	for i, row := range rows {
		entries[i] = models.Entry{
			ID:            FromPgUUID(row.ID),
			TransactionID: FromPgUUID(row.TransactionID),
			AccountID:     FromPgUUID(row.AccountID),
			Amount:        row.Amount,
			Direction:     row.Direction,
			CreatedAt:     row.CreatedAt.Time,
		}
	}
	return entries, nil
}
