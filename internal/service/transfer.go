package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/ayo6706/payment-multicurrency/internal/domain"
	"github.com/ayo6706/payment-multicurrency/internal/models"
	"github.com/ayo6706/payment-multicurrency/internal/observability"
	"github.com/ayo6706/payment-multicurrency/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// TransferService handles business logic for account transfers and exchanges.
type TransferService struct {
	repo    *repository.Repository
	db      *pgxpool.Pool
	fxRates ExchangeRateService
}

// NewTransferService creates a new TransferService instance.
func NewTransferService(repo *repository.Repository, db *pgxpool.Pool, fxRates ExchangeRateService) *TransferService {
	return &TransferService{
		repo:    repo,
		db:      db,
		fxRates: fxRates,
	}
}

// Transfer processes a same-currency transfer between two accounts.
// It handles idempotency, pessimistic locking to prevent deadlocks,
// balance validation, transaction creation, and ledger entry creation.
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

	queries := repository.New(s.db)

	// 0. Check Idempotency (simplistic check)
	existingTxRow, err := queries.CheckTransactionIdempotency(ctx, referenceID)
	if err == nil {
		return &models.Transaction{
			ID:          repository.FromPgUUID(existingTxRow.ID),
			Amount:      existingTxRow.Amount,
			Currency:    existingTxRow.Currency,
			Type:        existingTxRow.Type,
			Status:      existingTxRow.Status,
			ReferenceID: existingTxRow.ReferenceID,
			// CreatedAt: existingTxRow.CreatedAt.Time,
		}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("failed to check idempotency: %w", err)
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	qtx := queries.WithTx(tx)

	// 1. Lock accounts in a consistent order to prevent deadlocks
	account1ID, account2ID := fromAccountID, toAccountID
	if account1ID.String() > account2ID.String() {
		account1ID, account2ID = toAccountID, fromAccountID
	}

	if fromAccountID == account1ID {
		// Lock Sender first
		_, err = qtx.LockAccount(ctx, repository.ToPgUUID(account1ID))
		if err != nil {
			return nil, fmt.Errorf("failed to lock sender account: %w", err)
		}
		// Lock Receiver
		_, err = qtx.LockAccount(ctx, repository.ToPgUUID(account2ID))
		if err != nil {
			return nil, fmt.Errorf("failed to lock receiver account: %w", err)
		}
	} else {
		// Lock Receiver first
		_, err = qtx.LockAccount(ctx, repository.ToPgUUID(account1ID))
		if err != nil {
			return nil, fmt.Errorf("failed to lock receiver account: %w", err)
		}
		// Lock Sender
		_, err = qtx.LockAccount(ctx, repository.ToPgUUID(account2ID))
		if err != nil {
			return nil, fmt.Errorf("failed to lock sender account: %w", err)
		}
	}

	// 1.5 Fetch Account Details & Validate
	fromAccRow, err := qtx.GetAccountBalanceAndCurrency(ctx, repository.ToPgUUID(fromAccountID))
	if err != nil {
		return nil, fmt.Errorf("failed to fetch sender account: %w", err)
	}
	fromBalance, fromCurrency := fromAccRow.Balance, fromAccRow.Currency

	toCurrency, err := qtx.GetAccountCurrency(ctx, repository.ToPgUUID(toAccountID))
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
	_, err = qtx.CreateTransaction(ctx, repository.CreateTransactionParams{
		ID:          repository.ToPgUUID(transactionID),
		Amount:      amount,
		Currency:    fromCurrency,
		Type:        domain.TxTypeTransfer,
		Status:      domain.TxStatusCompleted,
		ReferenceID: referenceID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create transaction: %w", err)
	}

	// 3. Create Double Entries
	_, err = qtx.CreateEntry(ctx, repository.CreateEntryParams{
		ID:            repository.ToPgUUID(uuid.New()),
		TransactionID: repository.ToPgUUID(transactionID),
		AccountID:     repository.ToPgUUID(fromAccountID),
		Amount:        amount,
		Direction:     domain.DirectionDebit,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create sender ledger entry: %w", err)
	}

	_, err = qtx.CreateEntry(ctx, repository.CreateEntryParams{
		ID:            repository.ToPgUUID(uuid.New()),
		TransactionID: repository.ToPgUUID(transactionID),
		AccountID:     repository.ToPgUUID(toAccountID),
		Amount:        amount,
		Direction:     domain.DirectionCredit,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create receiver ledger entry: %w", err)
	}

	// 4. Update Balances
	err = qtx.UpdateAccountBalance(ctx, repository.UpdateAccountBalanceParams{
		Balance: -amount,
		ID:      repository.ToPgUUID(fromAccountID),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to update sender balance: %w", err)
	}

	err = qtx.UpdateAccountBalance(ctx, repository.UpdateAccountBalanceParams{
		Balance: amount,
		ID:      repository.ToPgUUID(toAccountID),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to update receiver balance: %w", err)
	}

	balanceAudit := map[string]int64{}
	balanceAudit[fromCurrency] -= amount
	balanceAudit[fromCurrency] += amount
	auditLedgerBalances(balanceAudit)

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return &models.Transaction{
		ID:          transactionID,
		Amount:      amount,
		Currency:    fromCurrency,
		Type:        domain.TxTypeTransfer,
		Status:      domain.TxStatusCompleted,
		ReferenceID: referenceID,
	}, nil
}

// TransferExchangeCmd holds the parameters for a cross-currency transfer exchange.
type TransferExchangeCmd struct {
	FromAccountID uuid.UUID
	ToAccountID   uuid.UUID
	Amount        int64
	FromCurrency  string
	ToCurrency    string
	ReferenceID   string
}

// TransferExchange processes a cross-currency transfer using the current exchange rate.
// It handles idempotency, multi-party pessimistic locking,
// balance valuation, foreign exchange, and entry logging.
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

	queries := repository.New(s.db)

	// 0. Check Idempotency
	existingTxRow, err := queries.CheckTransactionIdempotency(ctx, cmd.ReferenceID)
	if err == nil {
		var fxRate *decimal.Decimal
		if existingTxRow.FxRate.Valid {
			// Convert pgtype.Numeric back to decimal.Decimal or string if model.Transaction uses string.
			// Models uses *decimal.Decimal for FxRate. Let's parse the pgtype.Numeric.
			strVal, _ := existingTxRow.FxRate.Value()
			s := fmt.Sprintf("%v", strVal)
			dec, err := decimal.NewFromString(s)
			if err == nil {
				fxRate = &dec
			}
		}

		return &models.Transaction{
			ID:          repository.FromPgUUID(existingTxRow.ID),
			Amount:      existingTxRow.Amount,
			Currency:    existingTxRow.Currency,
			Type:        existingTxRow.Type,
			Status:      existingTxRow.Status,
			ReferenceID: existingTxRow.ReferenceID,
			FXRate:      fxRate,
			// CreatedAt: existingTxRow.CreatedAt.Time,
		}, nil
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
		return nil, fmt.Errorf("failed to identify liquidity source account: %w", err)
	}
	liqTargetID, err := getSystemAccountID(cmd.ToCurrency)
	if err != nil {
		return nil, fmt.Errorf("failed to identify liquidity target account: %w", err)
	}

	// 3. Begin Transaction
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	qtx := queries.WithTx(tx)

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
		_, err := qtx.LockAccount(ctx, repository.ToPgUUID(id))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, fmt.Errorf("account %s not found", id)
			}
			return nil, fmt.Errorf("failed to lock account %s: %w", id, err)
		}
	}

	// Lock Phase Complete

	// 5. Check Balances and Currencies
	fromAccRow, err := qtx.GetAccountBalanceAndCurrency(ctx, repository.ToPgUUID(cmd.FromAccountID))
	if err != nil {
		return nil, fmt.Errorf("failed to fetch sender account: %w", err)
	}
	fromBalance, fromAccountCurrency := fromAccRow.Balance, fromAccRow.Currency

	toAccountCurrency, err := qtx.GetAccountCurrency(ctx, repository.ToPgUUID(cmd.ToAccountID))
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
	fxRateVar := rate

	// pass fx rate to numeric
	var numericFxRate pgtype.Numeric
	err = numericFxRate.Scan(rate.String())
	if err != nil {
		return nil, fmt.Errorf("failed to parse fx rate: %w", err)
	}

	metadataJson := []byte(fmt.Sprintf(`{"from_currency": "%s", "to_currency": "%s", "target_amount": %d}`, cmd.FromCurrency, cmd.ToCurrency, amountTarget))

	_, err = qtx.CreateTransaction(ctx, repository.CreateTransactionParams{
		ID:          repository.ToPgUUID(transactionID),
		Amount:      amountSource,
		Currency:    cmd.FromCurrency,
		Type:        domain.TxTypeExchange,
		Status:      domain.TxStatusCompleted,
		ReferenceID: cmd.ReferenceID,
		FxRate:      numericFxRate,
		Metadata:    metadataJson,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create transaction: %w", err)
	}

	// 8. Create 4 Ledger Entries

	// Entry 1: Debit User (Source Currency)
	_, err = qtx.CreateEntry(ctx, repository.CreateEntryParams{
		ID:            repository.ToPgUUID(uuid.New()),
		TransactionID: repository.ToPgUUID(transactionID),
		AccountID:     repository.ToPgUUID(cmd.FromAccountID),
		Amount:        amountSource,
		Direction:     domain.DirectionDebit,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to log user debit entry: %w", err)
	}

	// Entry 2: Credit Liquidity (Source Currency)
	_, err = qtx.CreateEntry(ctx, repository.CreateEntryParams{
		ID:            repository.ToPgUUID(uuid.New()),
		TransactionID: repository.ToPgUUID(transactionID),
		AccountID:     repository.ToPgUUID(liqSourceID),
		Amount:        amountSource,
		Direction:     domain.DirectionCredit,
	})
	if err != nil {
		return nil, err
	}

	// Entry 3: Debit Liquidity (Target Currency)
	_, err = qtx.CreateEntry(ctx, repository.CreateEntryParams{
		ID:            repository.ToPgUUID(uuid.New()),
		TransactionID: repository.ToPgUUID(transactionID),
		AccountID:     repository.ToPgUUID(liqTargetID),
		Amount:        amountTarget,
		Direction:     domain.DirectionDebit,
	})
	if err != nil {
		return nil, err
	}

	// Entry 4: Credit User (Target Currency)
	_, err = qtx.CreateEntry(ctx, repository.CreateEntryParams{
		ID:            repository.ToPgUUID(uuid.New()),
		TransactionID: repository.ToPgUUID(transactionID),
		AccountID:     repository.ToPgUUID(cmd.ToAccountID),
		Amount:        amountTarget,
		Direction:     domain.DirectionCredit,
	})
	if err != nil {
		return nil, err
	}

	// 9. Update Balances
	// User Source Debit
	err = qtx.UpdateAccountBalance(ctx, repository.UpdateAccountBalanceParams{
		Balance: -amountSource,
		ID:      repository.ToPgUUID(cmd.FromAccountID),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to debit user account: %w", err)
	}
	// Liquidity Source Credit
	err = qtx.UpdateAccountBalance(ctx, repository.UpdateAccountBalanceParams{
		Balance: amountSource,
		ID:      repository.ToPgUUID(liqSourceID),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to credit liquidity source: %w", err)
	}
	// Liquidity Target Debit
	err = qtx.UpdateAccountBalance(ctx, repository.UpdateAccountBalanceParams{
		Balance: -amountTarget,
		ID:      repository.ToPgUUID(liqTargetID),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to debit liquidity target: %w", err)
	}
	// User Target Credit
	err = qtx.UpdateAccountBalance(ctx, repository.UpdateAccountBalanceParams{
		Balance: amountTarget,
		ID:      repository.ToPgUUID(cmd.ToAccountID),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to credit user account: %w", err)
	}

	fxAudit := map[string]int64{}
	fxAudit[cmd.FromCurrency] -= amountSource
	fxAudit[cmd.FromCurrency] += amountSource
	fxAudit[cmd.ToCurrency] -= amountTarget
	fxAudit[cmd.ToCurrency] += amountTarget
	auditLedgerBalances(fxAudit)

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return &models.Transaction{
		ID:          transactionID,
		Amount:      amountSource,
		Currency:    cmd.FromCurrency,
		Type:        domain.TxTypeExchange,
		Status:      domain.TxStatusCompleted,
		ReferenceID: cmd.ReferenceID,
		FXRate:      &fxRateVar,
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

func auditLedgerBalances(balances map[string]int64) {
	for currency, sum := range balances {
		if sum != 0 {
			observability.IncrementLedgerImbalance(currency)
		}
	}
}
