package repository

import (
	"context"
	"fmt"

	"github.com/ayo6706/payment-multicurrency/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

func (r *Repository) CreateUser(ctx context.Context, user *models.User) error {
	query := `INSERT INTO users (id, username, email, created_at) VALUES ($1, $2, $3, NOW()) RETURNING created_at`
	err := r.db.QueryRow(ctx, query, user.ID, user.Username, user.Email).Scan(&user.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to create user: %w", err)
	}
	return nil
}

func (r *Repository) CreateAccount(ctx context.Context, account *models.Account) error {
	query := `INSERT INTO accounts (id, user_id, currency, balance, created_at) VALUES ($1, $2, $3, $4, NOW()) RETURNING created_at`
	err := r.db.QueryRow(ctx, query, account.ID, account.UserID, account.Currency, account.Balance).Scan(&account.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to create account: %w", err)
	}
	return nil
}

func (r *Repository) GetAccount(ctx context.Context, id uuid.UUID) (*models.Account, error) {
	account := &models.Account{}
	query := `SELECT id, user_id, currency, balance, created_at FROM accounts WHERE id = $1`
	err := r.db.QueryRow(ctx, query, id).Scan(&account.ID, &account.UserID, &account.Currency, &account.Balance, &account.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to get account: %w", err)
	}
	return account, nil
}

func (r *Repository) GetEntries(ctx context.Context, accountID uuid.UUID, limit, offset int) ([]models.Entry, error) {
	query := `
		SELECT id, transaction_id, account_id, amount, direction, created_at
		FROM entries
		WHERE account_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`
	rows, err := r.db.Query(ctx, query, accountID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to get entries: %w", err)
	}
	defer rows.Close()

	var entries []models.Entry
	for rows.Next() {
		var e models.Entry
		if err := rows.Scan(&e.ID, &e.TransactionID, &e.AccountID, &e.Amount, &e.Direction, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan entry: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, nil
}
