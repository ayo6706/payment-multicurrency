package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/ayo6706/payment-multicurrency/internal/domain"
	"github.com/ayo6706/payment-multicurrency/internal/models"
	"github.com/ayo6706/payment-multicurrency/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type TransferService struct {
	repo    *repository.Repository
	db      *pgxpool.Pool
	fxRates ExchangeRateService
}

func NewTransferService(repo *repository.Repository, db *pgxpool.Pool, fxRates ExchangeRateService) *TransferService {
	return &TransferService{
		repo:    repo,
		db:      db,
		fxRates: fxRates,
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

	if fromAccountID == account1ID {
		// Lock Sender first
		_, err = tx.Exec(ctx, `SELECT 1 FROM accounts WHERE id = $1 FOR UPDATE`, account1ID)
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
		_, err = tx.Exec(ctx, `SELECT 1 FROM accounts WHERE id = $1 FOR UPDATE`, account2ID)
		if err != nil {
			return nil, err
		}
	}

	// Lock Phase Complete

	// 1.5 Fetch Account Details & Validate
	var fromBalance int64
	var fromCurrency, toCurrency string

	// Fetch Sender
	err = tx.QueryRow(ctx, `SELECT balance, currency FROM accounts WHERE id = $1`, fromAccountID).Scan(&fromBalance, &fromCurrency)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch sender account: %w", err)
	}
	// Fetch Receiver
	err = tx.QueryRow(ctx, `SELECT currency FROM accounts WHERE id = $1`, toAccountID).Scan(&toCurrency)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch receiver account: %w", err)
	}

	if fromCurrency != toCurrency {
		return nil, fmt.Errorf("currency mismatch: sender is %s, receiver is %s", fromCurrency, toCurrency)
	}

	if fromBalance < amount {
		return nil, models.ErrInsufficientFunds
	}

	// 2. Create Transaction Record
	transactionID := uuid.New()
	_, err = tx.Exec(ctx, `INSERT INTO transactions (id, amount, currency, type, status, reference_id, created_at) VALUES ($1, $2, $3, $4, $5, $6, NOW())`,
		transactionID, amount, fromCurrency, domain.TxTypeTransfer, domain.TxStatusCompleted, referenceID)
	if err != nil {
		return nil, err
	}

	// 3. Create Double Entries
	_, err = tx.Exec(ctx, `INSERT INTO entries (id, transaction_id, account_id, amount, direction, created_at) VALUES ($1, $2, $3, $4, $5, NOW())`,
		uuid.New(), transactionID, fromAccountID, amount, domain.DirectionDebit)
	if err != nil {
		return nil, err
	}

	_, err = tx.Exec(ctx, `INSERT INTO entries (id, transaction_id, account_id, amount, direction, created_at) VALUES ($1, $2, $3, $4, $5, NOW())`,
		uuid.New(), transactionID, toAccountID, amount, domain.DirectionCredit)
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
		Currency:    fromCurrency,
		Type:        domain.TxTypeTransfer,
		Status:      domain.TxStatusCompleted,
		ReferenceID: referenceID,
		// CreatedAt: time.Now(),
	}, nil
}

type TransferExchangeCmd struct {
	FromAccountID uuid.UUID
	ToAccountID   uuid.UUID
	Amount        int64
	FromCurrency  string
	ToCurrency    string
	ReferenceID   string
}

func (s *TransferService) TransferExchange(ctx context.Context, cmd TransferExchangeCmd) (*models.Transaction, error) {
	if cmd.Amount <= 0 {
		return nil, fmt.Errorf("invalid amount: %d", cmd.Amount)
	}
	if cmd.ReferenceID == "" {
		return nil, errors.New("reference_id is required")
	}
	if cmd.FromAccountID == cmd.ToAccountID {
		return nil, errors.New("cannot transfer to the same account")
	}
	if cmd.FromCurrency == cmd.ToCurrency {
		return nil, errors.New("source and target currency must be different")
	}

	// 0. Check Idempotency
	var existingTx models.Transaction
	row := s.db.QueryRow(ctx, `SELECT id, amount, currency, type, status, reference_id, fx_rate, created_at FROM transactions WHERE reference_id = $1`, cmd.ReferenceID)
	err := row.Scan(&existingTx.ID, &existingTx.Amount, &existingTx.Currency, &existingTx.Type, &existingTx.Status, &existingTx.ReferenceID, &existingTx.FXRate, &existingTx.CreatedAt)
	if err == nil {
		return &existingTx, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("failed to check idempotency: %w", err)
	}

	// 1. Get Exchange Rate
	rate, err := s.fxRates.GetExchangeRate(ctx, cmd.FromCurrency, cmd.ToCurrency)
	if err != nil {
		return nil, fmt.Errorf("failed to get exchange rate: %w", err)
	}

	// 2. Determine System Liquidity Accounts
	liqSourceID, err := getSystemAccountID(cmd.FromCurrency)
	if err != nil {
		return nil, err
	}
	liqTargetID, err := getSystemAccountID(cmd.ToCurrency)
	if err != nil {
		return nil, err
	}

	// 3. Begin Transaction
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// 4. Lock Accounts (Lock all 4 accounts involved)
	// Order: FromUser, LiqSource, LiqTarget, ToUser -> Sorted by ID
	accountIDs := []uuid.UUID{cmd.FromAccountID, liqSourceID, liqTargetID, cmd.ToAccountID}
	// Sort IDs
	for i := 0; i < len(accountIDs); i++ {
		for j := i + 1; j < len(accountIDs); j++ {
			if accountIDs[i].String() > accountIDs[j].String() {
				accountIDs[i], accountIDs[j] = accountIDs[j], accountIDs[i]
			}
		}
	}

	// Lock loop
	for _, id := range accountIDs {
		// Verify existence by scanning. Exec returns no error if 0 rows match.
		var lockedID uuid.UUID
		err := tx.QueryRow(ctx, `SELECT id FROM accounts WHERE id = $1 FOR UPDATE`, id).Scan(&lockedID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, fmt.Errorf("account %s not found", id)
			}
			return nil, fmt.Errorf("failed to lock account %s: %w", id, err)
		}
	}

	// Lock Phase Complete

	// 5. Check Balances and Currencies
	var fromBalance int64
	var fromAccountCurrency, toAccountCurrency string

	// Fetch Sender Details
	err = tx.QueryRow(ctx, `SELECT balance, currency FROM accounts WHERE id = $1`, cmd.FromAccountID).Scan(&fromBalance, &fromAccountCurrency)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch sender account: %w", err)
	}
	// Fetch Receiver Details
	err = tx.QueryRow(ctx, `SELECT currency FROM accounts WHERE id = $1`, cmd.ToAccountID).Scan(&toAccountCurrency)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch receiver account: %w", err)
	}

	// Validate Currencies
	if fromAccountCurrency != cmd.FromCurrency {
		return nil, fmt.Errorf("sender account currency (%s) does not match requested from_currency (%s)", fromAccountCurrency, cmd.FromCurrency)
	}
	if toAccountCurrency != cmd.ToCurrency {
		return nil, fmt.Errorf("receiver account currency (%s) does not match requested to_currency (%s)", toAccountCurrency, cmd.ToCurrency)
	}

	if fromBalance < cmd.Amount {
		return nil, models.ErrInsufficientFunds
	}

	// 6. Calculate Amounts
	sourceMoney := domain.NewMoney(cmd.Amount, cmd.FromCurrency)
	targetMoney := sourceMoney.Convert(cmd.ToCurrency, rate) // Convert uses rate (Target/Source)

	amountSource := sourceMoney.Amount
	amountTarget := targetMoney.Amount

	// 7. Create Transaction Record
	transactionID := uuid.New()
	_, err = tx.Exec(ctx, `INSERT INTO transactions (id, amount, currency, type, status, reference_id, fx_rate, metadata, created_at) 
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW())`,
		transactionID, amountSource, cmd.FromCurrency, domain.TxTypeExchange, domain.TxStatusCompleted, cmd.ReferenceID, rate, map[string]any{
			"from_currency": cmd.FromCurrency,
			"to_currency":   cmd.ToCurrency,
			"target_amount": amountTarget,
		})
	if err != nil {
		return nil, err
	}

	// 8. Create 4 Ledger Entries

	// Entry 1: Debit User (Source Currency)
	_, err = tx.Exec(ctx, `INSERT INTO entries (id, transaction_id, account_id, amount, direction, created_at) VALUES ($1, $2, $3, $4, $5, NOW())`,
		uuid.New(), transactionID, cmd.FromAccountID, amountSource, domain.DirectionDebit)
	if err != nil {
		return nil, err
	}

	// Entry 2: Credit Liquidity (Source Currency)
	_, err = tx.Exec(ctx, `INSERT INTO entries (id, transaction_id, account_id, amount, direction, created_at) VALUES ($1, $2, $3, $4, $5, NOW())`,
		uuid.New(), transactionID, liqSourceID, amountSource, domain.DirectionCredit)
	if err != nil {
		return nil, err
	}

	// Entry 3: Debit Liquidity (Target Currency)
	_, err = tx.Exec(ctx, `INSERT INTO entries (id, transaction_id, account_id, amount, direction, created_at) VALUES ($1, $2, $3, $4, $5, NOW())`,
		uuid.New(), transactionID, liqTargetID, amountTarget, domain.DirectionDebit)
	if err != nil {
		return nil, err
	}

	// Entry 4: Credit User (Target Currency)
	_, err = tx.Exec(ctx, `INSERT INTO entries (id, transaction_id, account_id, amount, direction, created_at) VALUES ($1, $2, $3, $4, $5, NOW())`,
		uuid.New(), transactionID, cmd.ToAccountID, amountTarget, domain.DirectionCredit)
	if err != nil {
		return nil, err
	}

	// 9. Update Balances
	// User Source Debit
	_, err = tx.Exec(ctx, `UPDATE accounts SET balance = balance - $1 WHERE id = $2`, amountSource, cmd.FromAccountID)
	if err != nil {
		return nil, err
	}
	// Liquidity Source Credit
	_, err = tx.Exec(ctx, `UPDATE accounts SET balance = balance + $1 WHERE id = $2`, amountSource, liqSourceID)
	if err != nil {
		return nil, err
	}
	// Liquidity Target Debit
	// NOTE: System liquidity accounts are allowed to go negative (representing a short position).
	// No balance check required here.
	_, err = tx.Exec(ctx, `UPDATE accounts SET balance = balance - $1 WHERE id = $2`, amountTarget, liqTargetID)
	if err != nil {
		return nil, err
	}
	// User Target Credit
	_, err = tx.Exec(ctx, `UPDATE accounts SET balance = balance + $1 WHERE id = $2`, amountTarget, cmd.ToAccountID)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return &models.Transaction{
		ID:          transactionID,
		Amount:      amountSource,
		Currency:    cmd.FromCurrency,
		Type:        domain.TxTypeExchange,
		Status:      domain.TxStatusCompleted,
		ReferenceID: cmd.ReferenceID,
		FXRate:      &rate,
	}, nil
}

func getSystemAccountID(currency string) (uuid.UUID, error) {
	var idStr string
	switch currency {
	case "USD":
		idStr = domain.SystemAccountUSD
	case "EUR":
		idStr = domain.SystemAccountEUR
	case "GBP":
		idStr = domain.SystemAccountGBP
	default:
		return uuid.Nil, fmt.Errorf("unsupported currency for system liquidity: %s", currency)
	}
	return uuid.Parse(idStr)
}
