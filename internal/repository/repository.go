package repository

import (
	"context"
	"fmt"

	"github.com/ayo6706/payment-multicurrency/internal/models"
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
