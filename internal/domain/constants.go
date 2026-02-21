package domain

// System IDs (Must match migration 000003)
const (
	SystemUserID = "11111111-1111-1111-1111-111111111111"

	SystemAccountUSD = "22222222-2222-2222-2222-222222222222"
	SystemAccountEUR = "33333333-3333-3333-3333-333333333333"
	SystemAccountGBP = "44444444-4444-4444-4444-444444444444"

	DirectionDebit  = "debit"
	DirectionCredit = "credit"

	TxTypeTransfer = "transfer"
	TxTypeExchange = "exchange"
	TxTypePayout   = "payout"
	TxTypeDeposit  = "deposit"

	TxStatusCompleted  = "COMPLETED"
	TxStatusFailed     = "FAILED"
	TxStatusPending    = "PENDING"
	TxStatusProcessing = "PROCESSING"
	TxStatusReversed   = "REVERSED"

	// Payout statuses
	PayoutStatusPending      = "PENDING"
	PayoutStatusProcessing   = "PROCESSING"
	PayoutStatusCompleted    = "COMPLETED"
	PayoutStatusFailed       = "FAILED"
	PayoutStatusManualReview = "MANUAL_REVIEW"
)
