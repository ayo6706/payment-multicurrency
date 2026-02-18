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

func (s *TransferService) Transfer(ctx context.Context, fromAccountID, toAccountID uuid.UUID, amount int64, referenceID string) error {
	if amount <= 0 {
		return fmt.Errorf("invalid amount: %d", amount)
	}
	if referenceID == "" {
		return errors.New("reference_id is required")
	}
	if fromAccountID == toAccountID {
		return errors.New("cannot transfer to the same account")
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// 1. Lock accounts in a consistent order to prevent deadlocks
	account1ID, account2ID := fromAccountID, toAccountID
	if account1ID.String() > account2ID.String() {
		account1ID, account2ID = toAccountID, fromAccountID
	}

	// Always lock account1 first, then account2
	// We still need to know which one is the sender to check the balance
	var senderBalance int64

	if fromAccountID == account1ID {
		// Lock Sender first
		err = tx.QueryRow(ctx, `SELECT balance FROM accounts WHERE id = $1 FOR UPDATE`, account1ID).Scan(&senderBalance)
		if err != nil {
			return err
		}
		// Lock Receiver
		_, err = tx.Exec(ctx, `SELECT 1 FROM accounts WHERE id = $1 FOR UPDATE`, account2ID)
		if err != nil {
			return err
		}
	} else {
		// Lock Receiver first (because it has the smaller ID)
		_, err = tx.Exec(ctx, `SELECT 1 FROM accounts WHERE id = $1 FOR UPDATE`, account1ID)
		if err != nil {
			return err
		}
		// Lock Sender
		err = tx.QueryRow(ctx, `SELECT balance FROM accounts WHERE id = $1 FOR UPDATE`, account2ID).Scan(&senderBalance)
		if err != nil {
			return err
		}
	}

	if senderBalance < amount {
		return models.ErrInsufficientFunds
	}

	// 2. Create Transaction Record
	transactionID := uuid.New()
	_, err = tx.Exec(ctx, `INSERT INTO transactions (id, amount, currency, type, status, reference_id, created_at) VALUES ($1, $2, $3, $4, $5, $6, NOW())`,
		transactionID, amount, "USD", "transfer", "completed", referenceID) // Assuming USD for now, simplistic
	if err != nil {
		return err
	}

	// 3. Create Double Entries
	// Debit Sender
	_, err = tx.Exec(ctx, `INSERT INTO entries (id, transaction_id, account_id, amount, direction, created_at) VALUES ($1, $2, $3, $4, $5, NOW())`,
		uuid.New(), transactionID, fromAccountID, amount, "debit")
	if err != nil {
		return err
	}

	// Credit Receiver
	_, err = tx.Exec(ctx, `INSERT INTO entries (id, transaction_id, account_id, amount, direction, created_at) VALUES ($1, $2, $3, $4, $5, NOW())`,
		uuid.New(), transactionID, toAccountID, amount, "credit")
	if err != nil {
		return err
	}

	// 4. Update Balances
	_, err = tx.Exec(ctx, `UPDATE accounts SET balance = balance - $1 WHERE id = $2`, amount, fromAccountID)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `UPDATE accounts SET balance = balance + $1 WHERE id = $2`, amount, toAccountID)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}
