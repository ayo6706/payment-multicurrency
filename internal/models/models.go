package models

import (
	"time"

	"github.com/google/uuid"
)

type User struct {
	ID        uuid.UUID `json:"id"`
	Username  string    `json:"username"`
	Email     string    `json:"email"`
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
	ID          uuid.UUID `json:"id"`
	Amount      int64     `json:"amount"`
	Currency    string    `json:"currency"`
	Type        string    `json:"type"`   // e.g., "transfer"
	Status      string    `json:"status"` // e.g., "pending", "completed", "failed"
	ReferenceID string    `json:"reference_id"`
	CreatedAt   time.Time `json:"created_at"`
}

type Entry struct {
	ID            uuid.UUID `json:"id"`
	TransactionID uuid.UUID `json:"transaction_id"`
	AccountID     uuid.UUID `json:"account_id"`
	Amount        int64     `json:"amount"`
	Direction     string    `json:"direction"` // "debit" or "credit"
	CreatedAt     time.Time `json:"created_at"`
}
