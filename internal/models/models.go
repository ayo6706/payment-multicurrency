package models

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

var (
	ErrInsufficientFunds   = errors.New("insufficient funds")
	ErrUnsupportedCurrency = errors.New("unsupported currency")
	ErrRateUnavailable     = errors.New("exchange rate unavailable")
)

type User struct {
	ID        uuid.UUID `json:"id"`
	Username  string    `json:"username"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

type Account struct {
	ID        uuid.UUID `json:"id"`
	UserID    uuid.UUID `json:"user_id"`
	Currency  string    `json:"currency"`
	Balance   int64     `json:"balance"`
	CreatedAt time.Time `json:"created_at"`
}

type Transaction struct {
	ID          uuid.UUID        `json:"id"`
	Amount      int64            `json:"amount"`
	Currency    string           `json:"currency"`
	Type        string           `json:"type"`   // e.g., "transfer"
	Status      string           `json:"status"` // e.g., "pending", "completed", "failed"
	ReferenceID string           `json:"reference_id"`
	FXRate      *decimal.Decimal `json:"fx_rate,omitempty"` // populated for FX_EXCHANGE type
	Metadata    map[string]any   `json:"metadata,omitempty"`
	CreatedAt   time.Time        `json:"created_at"`
}

type Entry struct {
	ID            uuid.UUID `json:"id"`
	TransactionID uuid.UUID `json:"transaction_id"`
	AccountID     uuid.UUID `json:"account_id"`
	Amount        int64     `json:"amount"`
	Direction     string    `json:"direction"` // "debit" or "credit"
	CreatedAt     time.Time `json:"created_at"`
}

type Payout struct {
	ID            uuid.UUID `json:"id"`
	TransactionID uuid.UUID `json:"transaction_id"`
	AccountID     uuid.UUID `json:"account_id"`
	AmountMicros  int64     `json:"amount_micros"`
	Currency      string    `json:"currency"`
	Status        string    `json:"status"`
	GatewayRef    *string   `json:"gateway_ref,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}
