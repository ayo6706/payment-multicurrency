package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/ayo6706/payment-multicurrency/internal/domain"
	"github.com/ayo6706/payment-multicurrency/internal/models"
	"github.com/ayo6706/payment-multicurrency/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
)

var (
	// ErrInvalidAmount indicates a non-positive transfer amount.
	ErrInvalidAmount = errors.New("amount must be greater than zero")
	// ErrReferenceRequired indicates a missing idempotency reference.
	ErrReferenceRequired = errors.New("reference_id is required")
	// ErrSameAccountTransfer indicates source and destination are the same account.
	ErrSameAccountTransfer = errors.New("cannot transfer to the same account")
	// ErrCurrencyMismatch indicates source and destination account currencies differ.
	ErrCurrencyMismatch = errors.New("currency mismatch")
	// ErrSameCurrencyExchange indicates exchange request currencies are identical.
	ErrSameCurrencyExchange = errors.New("source and target currency must be different")
)

// TransferService handles business logic for account transfers and exchanges.
type TransferService struct {
	store   QueryStore
	fxRates ExchangeRateService
	audit   *AuditService
}

// NewTransferService creates a new TransferService instance.
func NewTransferService(store QueryStore, fxRates ExchangeRateService) *TransferService {
	return &TransferService{
		store:   store,
		fxRates: fxRates,
		audit:   NewAuditService(store),
	}
}

// Transfer processes a same-currency transfer between two accounts.
// It handles idempotency, pessimistic locking to prevent deadlocks,
// balance validation, transaction creation, and ledger entry creation.
func (s *TransferService) Transfer(ctx context.Context, fromAccountID, toAccountID uuid.UUID, amount int64, referenceID string) (*models.Transaction, error) {
	if amount <= 0 {
		return nil, ErrInvalidAmount
	}
	if referenceID == "" {
		return nil, ErrReferenceRequired
	}
	if fromAccountID == toAccountID {
		return nil, ErrSameAccountTransfer
	}

	queries := s.store.Queries()

	existingTxRow, err := queries.CheckTransactionIdempotency(ctx, referenceID)
	if err == nil {
		return mapExistingTransaction(existingTxRow), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("failed to check idempotency: %w", err)
	}

	transactionID := uuid.New()
	txCurrency := ""
	err = s.store.RunInTx(ctx, func(qtx *repository.Queries) error {
		// Lock accounts in a stable order to reduce deadlock risk.
		account1ID, account2ID := fromAccountID, toAccountID
		if account1ID.String() > account2ID.String() {
			account1ID, account2ID = toAccountID, fromAccountID
		}

		_, err = qtx.LockAccount(ctx, repository.ToPgUUID(account1ID))
		if err != nil {
			return fmt.Errorf("failed to lock account %s: %w", account1ID, err)
		}
		_, err = qtx.LockAccount(ctx, repository.ToPgUUID(account2ID))
		if err != nil {
			return fmt.Errorf("failed to lock account %s: %w", account2ID, err)
		}

		fromAccRow, err := qtx.GetAccountBalanceAndCurrency(ctx, repository.ToPgUUID(fromAccountID))
		if err != nil {
			return fmt.Errorf("failed to fetch sender account: %w", err)
		}
		fromBalance, fromCurrency := fromAccRow.Balance, fromAccRow.Currency
		txCurrency = fromCurrency

		toCurrency, err := qtx.GetAccountCurrency(ctx, repository.ToPgUUID(toAccountID))
		if err != nil {
			return fmt.Errorf("failed to fetch receiver account: %w", err)
		}

		if fromCurrency != toCurrency {
			return fmt.Errorf("%w: sender is %s, receiver is %s", ErrCurrencyMismatch, fromCurrency, toCurrency)
		}

		if fromBalance < amount {
			return models.ErrInsufficientFunds
		}

		_, err = qtx.CreateTransaction(ctx, repository.CreateTransactionParams{
			ID:          repository.ToPgUUID(transactionID),
			Amount:      amount,
			Currency:    txCurrency,
			Type:        domain.TxTypeTransfer,
			Status:      domain.TxStatusPending,
			ReferenceID: referenceID,
		})
		if err != nil {
			return fmt.Errorf("failed to create transaction: %w", err)
		}
		if err := s.audit.Write(ctx, qtx, "transaction", transactionID, nil, "created", "", domain.TxStatusPending, nil); err != nil {
			return err
		}
		if err := transitionTransactionState(ctx, qtx, s.audit, transactionID, domain.TxStatusProcessing, nil, "processing_started", nil); err != nil {
			return fmt.Errorf("failed to transition transaction to processing: %w", err)
		}

		_, err = qtx.CreateEntry(ctx, repository.CreateEntryParams{
			ID:            repository.ToPgUUID(uuid.New()),
			TransactionID: repository.ToPgUUID(transactionID),
			AccountID:     repository.ToPgUUID(fromAccountID),
			Amount:        amount,
			Direction:     domain.DirectionDebit,
		})
		if err != nil {
			return fmt.Errorf("failed to create sender ledger entry: %w", err)
		}

		_, err = qtx.CreateEntry(ctx, repository.CreateEntryParams{
			ID:            repository.ToPgUUID(uuid.New()),
			TransactionID: repository.ToPgUUID(transactionID),
			AccountID:     repository.ToPgUUID(toAccountID),
			Amount:        amount,
			Direction:     domain.DirectionCredit,
		})
		if err != nil {
			return fmt.Errorf("failed to create receiver ledger entry: %w", err)
		}

		rows, err := qtx.UpdateAccountBalance(ctx, repository.UpdateAccountBalanceParams{
			Balance: -amount,
			ID:      repository.ToPgUUID(fromAccountID),
		})
		if err != nil {
			return fmt.Errorf("failed to update sender balance: %w", err)
		}
		if err := requireExactlyOne(rows, "debit sender account"); err != nil {
			return err
		}

		rows, err = qtx.UpdateAccountBalance(ctx, repository.UpdateAccountBalanceParams{
			Balance: amount,
			ID:      repository.ToPgUUID(toAccountID),
		})
		if err != nil {
			return fmt.Errorf("failed to update receiver balance: %w", err)
		}
		if err := requireExactlyOne(rows, "credit receiver account"); err != nil {
			return err
		}
		if err := transitionTransactionState(ctx, qtx, s.audit, transactionID, domain.TxStatusCompleted, nil, "completed", nil); err != nil {
			return fmt.Errorf("failed to complete transaction: %w", err)
		}
		return nil
	})
	if err != nil {
		if isUniqueViolation(err) {
			existing, lookupErr := queries.CheckTransactionIdempotency(ctx, referenceID)
			if lookupErr == nil {
				return mapExistingTransaction(existing), nil
			}
		}
		return nil, err
	}

	return &models.Transaction{
		ID:          transactionID,
		Amount:      amount,
		Currency:    txCurrency,
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
		return nil, ErrInvalidAmount
	}
	if cmd.ReferenceID == "" {
		return nil, ErrReferenceRequired
	}
	if cmd.FromAccountID == cmd.ToAccountID {
		return nil, ErrSameAccountTransfer
	}
	if cmd.FromCurrency == cmd.ToCurrency {
		return nil, ErrSameCurrencyExchange
	}

	queries := s.store.Queries()

	// 0. Check Idempotency
	existingTxRow, err := queries.CheckTransactionIdempotency(ctx, cmd.ReferenceID)
	if err == nil {
		return mapExistingTransaction(existingTxRow), nil
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

	transactionID := uuid.New()
	fxRateVar := rate

	err = s.store.RunInTx(ctx, func(qtx *repository.Queries) error {
		accountIDs := dedupeUUIDs(cmd.FromAccountID, liqSourceID, liqTargetID, cmd.ToAccountID)
		sortUUIDs(accountIDs)
		for _, id := range accountIDs {
			_, err := qtx.LockAccount(ctx, repository.ToPgUUID(id))
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return fmt.Errorf("account %s not found", id)
				}
				return fmt.Errorf("failed to lock account %s: %w", id, err)
			}
		}

		fromAccRow, err := qtx.GetAccountBalanceAndCurrency(ctx, repository.ToPgUUID(cmd.FromAccountID))
		if err != nil {
			return fmt.Errorf("failed to fetch sender account: %w", err)
		}
		fromBalance, fromAccountCurrency := fromAccRow.Balance, fromAccRow.Currency

		toAccountCurrency, err := qtx.GetAccountCurrency(ctx, repository.ToPgUUID(cmd.ToAccountID))
		if err != nil {
			return fmt.Errorf("failed to fetch receiver account: %w", err)
		}

		// Validate Currencies
		if fromAccountCurrency != cmd.FromCurrency {
			return fmt.Errorf("sender account currency (%s) does not match requested from_currency (%s)", fromAccountCurrency, cmd.FromCurrency)
		}
		if toAccountCurrency != cmd.ToCurrency {
			return fmt.Errorf("receiver account currency (%s) does not match requested to_currency (%s)", toAccountCurrency, cmd.ToCurrency)
		}

		if fromBalance < cmd.Amount {
			return models.ErrInsufficientFunds
		}

		_, err = qtx.CreateTransaction(ctx, repository.CreateTransactionParams{
			ID:          repository.ToPgUUID(transactionID),
			Amount:      cmd.Amount,
			Currency:    cmd.FromCurrency,
			Type:        domain.TxTypeExchange,
			Status:      domain.TxStatusPending,
			ReferenceID: cmd.ReferenceID,
		})
		if err != nil {
			return fmt.Errorf("failed to create transaction: %w", err)
		}
		if err := s.audit.Write(ctx, qtx, "transaction", transactionID, nil, "created", "", domain.TxStatusPending, nil); err != nil {
			return err
		}
		if err := transitionTransactionState(ctx, qtx, s.audit, transactionID, domain.TxStatusProcessing, nil, "processing_started", nil); err != nil {
			return fmt.Errorf("failed to transition transaction to processing: %w", err)
		}

		sourceMoney := domain.NewMoney(cmd.Amount, cmd.FromCurrency)
		targetMoney := sourceMoney.Convert(cmd.ToCurrency, rate)

		amountSource := sourceMoney.Amount
		amountTarget := targetMoney.Amount

		var numericFxRate pgtype.Numeric
		err = numericFxRate.Scan(rate.String())
		if err != nil {
			return fmt.Errorf("failed to parse fx rate: %w", err)
		}

		metadataJson, err := json.Marshal(map[string]any{
			"from_currency": cmd.FromCurrency,
			"to_currency":   cmd.ToCurrency,
			"target_amount": amountTarget,
		})
		if err != nil {
			return fmt.Errorf("failed to encode exchange metadata: %w", err)
		}

		rows, err := qtx.UpdateTransactionFx(ctx, repository.UpdateTransactionFxParams{
			FxRate:   numericFxRate,
			Metadata: metadataJson,
			ID:       repository.ToPgUUID(transactionID),
		})
		if err != nil {
			return fmt.Errorf("failed to update transaction fx metadata: %w", err)
		}
		if err := requireExactlyOne(rows, "update transaction fx metadata"); err != nil {
			return err
		}

		_, err = qtx.CreateEntry(ctx, repository.CreateEntryParams{
			ID:            repository.ToPgUUID(uuid.New()),
			TransactionID: repository.ToPgUUID(transactionID),
			AccountID:     repository.ToPgUUID(cmd.FromAccountID),
			Amount:        amountSource,
			Direction:     domain.DirectionDebit,
		})
		if err != nil {
			return fmt.Errorf("failed to log user debit entry: %w", err)
		}

		_, err = qtx.CreateEntry(ctx, repository.CreateEntryParams{
			ID:            repository.ToPgUUID(uuid.New()),
			TransactionID: repository.ToPgUUID(transactionID),
			AccountID:     repository.ToPgUUID(liqSourceID),
			Amount:        amountSource,
			Direction:     domain.DirectionCredit,
		})
		if err != nil {
			return err
		}

		_, err = qtx.CreateEntry(ctx, repository.CreateEntryParams{
			ID:            repository.ToPgUUID(uuid.New()),
			TransactionID: repository.ToPgUUID(transactionID),
			AccountID:     repository.ToPgUUID(liqTargetID),
			Amount:        amountTarget,
			Direction:     domain.DirectionDebit,
		})
		if err != nil {
			return err
		}

		_, err = qtx.CreateEntry(ctx, repository.CreateEntryParams{
			ID:            repository.ToPgUUID(uuid.New()),
			TransactionID: repository.ToPgUUID(transactionID),
			AccountID:     repository.ToPgUUID(cmd.ToAccountID),
			Amount:        amountTarget,
			Direction:     domain.DirectionCredit,
		})
		if err != nil {
			return err
		}

		rows, err = qtx.UpdateAccountBalance(ctx, repository.UpdateAccountBalanceParams{
			Balance: -amountSource,
			ID:      repository.ToPgUUID(cmd.FromAccountID),
		})
		if err != nil {
			return fmt.Errorf("failed to debit user account: %w", err)
		}
		if err := requireExactlyOne(rows, "debit source account"); err != nil {
			return err
		}
		rows, err = qtx.UpdateAccountBalance(ctx, repository.UpdateAccountBalanceParams{
			Balance: amountSource,
			ID:      repository.ToPgUUID(liqSourceID),
		})
		if err != nil {
			return fmt.Errorf("failed to credit liquidity source: %w", err)
		}
		if err := requireExactlyOne(rows, "credit source liquidity account"); err != nil {
			return err
		}
		rows, err = qtx.UpdateAccountBalance(ctx, repository.UpdateAccountBalanceParams{
			Balance: -amountTarget,
			ID:      repository.ToPgUUID(liqTargetID),
		})
		if err != nil {
			return fmt.Errorf("failed to debit liquidity target: %w", err)
		}
		if err := requireExactlyOne(rows, "debit target liquidity account"); err != nil {
			return err
		}
		rows, err = qtx.UpdateAccountBalance(ctx, repository.UpdateAccountBalanceParams{
			Balance: amountTarget,
			ID:      repository.ToPgUUID(cmd.ToAccountID),
		})
		if err != nil {
			return fmt.Errorf("failed to credit user account: %w", err)
		}
		if err := requireExactlyOne(rows, "credit destination account"); err != nil {
			return err
		}
		if err := transitionTransactionState(ctx, qtx, s.audit, transactionID, domain.TxStatusCompleted, nil, "completed", nil); err != nil {
			return fmt.Errorf("failed to complete exchange transaction: %w", err)
		}
		return nil
	})
	if err != nil {
		if isUniqueViolation(err) {
			existing, lookupErr := queries.CheckTransactionIdempotency(ctx, cmd.ReferenceID)
			if lookupErr == nil {
				return mapExistingTransaction(existing), nil
			}
		}
		return nil, err
	}

	return &models.Transaction{
		ID:          transactionID,
		Amount:      cmd.Amount,
		Currency:    cmd.FromCurrency,
		Type:        domain.TxTypeExchange,
		Status:      domain.TxStatusCompleted,
		ReferenceID: cmd.ReferenceID,
		FXRate:      &fxRateVar,
	}, nil
}

func getSystemAccountID(currency string) (uuid.UUID, error) {
	switch strings.ToUpper(strings.TrimSpace(currency)) {
	case "USD":
		return uuid.Parse(domain.SystemAccountUSD)
	case "EUR":
		return uuid.Parse(domain.SystemAccountEUR)
	case "GBP":
		return uuid.Parse(domain.SystemAccountGBP)
	default:
		return uuid.Nil, fmt.Errorf("unsupported currency for system liquidity: %s", currency)
	}
}

func mapExistingTransaction(row repository.CheckTransactionIdempotencyRow) *models.Transaction {
	var fxRate *decimal.Decimal
	if row.FxRate.Valid {
		val, err := row.FxRate.Value()
		if err == nil {
			dec, parseErr := decimal.NewFromString(fmt.Sprintf("%v", val))
			if parseErr == nil {
				fxRate = &dec
			}
		}
	}
	return &models.Transaction{
		ID:          repository.FromPgUUID(row.ID),
		Amount:      row.Amount,
		Currency:    row.Currency,
		Type:        row.Type,
		Status:      row.Status,
		ReferenceID: row.ReferenceID,
		FXRate:      fxRate,
	}
}

func sortUUIDs(ids []uuid.UUID) {
	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			if ids[i].String() > ids[j].String() {
				ids[i], ids[j] = ids[j], ids[i]
			}
		}
	}
}

func dedupeUUIDs(ids ...uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]struct{}, len(ids))
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
