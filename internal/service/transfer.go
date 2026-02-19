package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/ayo6706/payment-multicurrency/internal/models"
	"github.com/ayo6706/payment-multicurrency/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type TransferService struct {
	repo *repository.Repository
	db   *pgxpool.Pool
}

func NewTransferService(repo *repository.Repository, db *pgxpool.Pool) *TransferService {
	return &TransferService{
		repo: repo,
		db:   db,
	}
}

func (s *TransferService) Transfer(ctx context.Context, fromAccountID, toAccountID uuid.UUID, amount int64, referenceID string) (*models.Transaction, error) {
	if amount <= 0 {
		return nil, fmt.Errorf("invalid amount: %d", amount)
	}
	if referenceID == "" {
		return nil, errors.New("reference_id is required")
	}
	if fromAccountID == toAccountID {
		return nil, errors.New("cannot transfer to the same account")
	}

	// 0. Check Idempotency (simplistic check)
	// For stricter correctness we might want to do this inside the TX or use INSERT ON CONFLICT DO NOTHING RETURNING...
	var existingTx models.Transaction

	// Check if transaction already exists
	row := s.db.QueryRow(ctx, `SELECT id, amount, currency, type, status, reference_id, created_at FROM transactions WHERE reference_id = $1`, referenceID)
	err := row.Scan(&existingTx.ID, &existingTx.Amount, &existingTx.Currency, &existingTx.Type, &existingTx.Status, &existingTx.ReferenceID, &existingTx.CreatedAt)
	if err == nil {
		return &existingTx, nil
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// 1. Lock accounts in a consistent order to prevent deadlocks
	account1ID, account2ID := fromAccountID, toAccountID
	if account1ID.String() > account2ID.String() {
		account1ID, account2ID = toAccountID, fromAccountID
	}

	var senderBalance int64

	if fromAccountID == account1ID {
		// Lock Sender first
		err = tx.QueryRow(ctx, `SELECT balance FROM accounts WHERE id = $1 FOR UPDATE`, account1ID).Scan(&senderBalance)
		if err != nil {
			return nil, err
		}
		// Lock Receiver
		_, err = tx.Exec(ctx, `SELECT 1 FROM accounts WHERE id = $1 FOR UPDATE`, account2ID)
		if err != nil {
			return nil, err
		}
	} else {
		// Lock Receiver first
		_, err = tx.Exec(ctx, `SELECT 1 FROM accounts WHERE id = $1 FOR UPDATE`, account1ID)
		if err != nil {
			return nil, err
		}
		// Lock Sender
		err = tx.QueryRow(ctx, `SELECT balance FROM accounts WHERE id = $1 FOR UPDATE`, account2ID).Scan(&senderBalance)
		if err != nil {
			return nil, err
		}
	}

	if senderBalance < amount {
		return nil, models.ErrInsufficientFunds
	}

	// 2. Create Transaction Record
	transactionID := uuid.New()
	_, err = tx.Exec(ctx, `INSERT INTO transactions (id, amount, currency, type, status, reference_id, created_at) VALUES ($1, $2, $3, $4, $5, $6, NOW())`,
		transactionID, amount, "USD", "transfer", "completed", referenceID)
	if err != nil {
		return nil, err
	}

	// 3. Create Double Entries
	_, err = tx.Exec(ctx, `INSERT INTO entries (id, transaction_id, account_id, amount, direction, created_at) VALUES ($1, $2, $3, $4, $5, NOW())`,
		uuid.New(), transactionID, fromAccountID, amount, "debit")
	if err != nil {
		return nil, err
	}

	_, err = tx.Exec(ctx, `INSERT INTO entries (id, transaction_id, account_id, amount, direction, created_at) VALUES ($1, $2, $3, $4, $5, NOW())`,
		uuid.New(), transactionID, toAccountID, amount, "credit")
	if err != nil {
		return nil, err
	}

	// 4. Update Balances
	_, err = tx.Exec(ctx, `UPDATE accounts SET balance = balance - $1 WHERE id = $2`, amount, fromAccountID)
	if err != nil {
		return nil, err
	}

	_, err = tx.Exec(ctx, `UPDATE accounts SET balance = balance + $1 WHERE id = $2`, amount, toAccountID)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return &models.Transaction{
		ID:          transactionID,
		Amount:      amount,
		Currency:    "USD",
		Type:        "transfer",
		Status:      "completed",
		ReferenceID: referenceID,
		// CreatedAt: time.Now(),
	}, nil
}
