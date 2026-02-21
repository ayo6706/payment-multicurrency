package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/ayo6706/payment-multicurrency/internal/repository"
	"github.com/google/uuid"
)

var transactionTransitions = map[string]map[string]struct{}{
	"PENDING": {
		"PROCESSING": {},
		"FAILED":     {},
	},
	"PROCESSING": {
		"PENDING":   {},
		"COMPLETED": {},
		"FAILED":    {},
		"REVERSED":  {},
	},
	"COMPLETED": {
		"REVERSED": {},
	},
	"FAILED": {
		"PROCESSING": {},
		"REVERSED":   {},
	},
	"REVERSED": {},
}

func normalizeState(state string) string {
	return strings.ToUpper(strings.TrimSpace(state))
}

func canTransition(current, next string) bool {
	current = normalizeState(current)
	next = normalizeState(next)
	nextStates, ok := transactionTransitions[current]
	if !ok {
		return false
	}
	_, ok = nextStates[next]
	return ok
}

func transitionTransactionState(ctx context.Context, qtx *repository.Queries, audit *AuditService, transactionID uuid.UUID, nextState string, actorID *uuid.UUID, action string, metadata []byte) error {
	currentState, err := qtx.GetTransactionStatusForUpdate(ctx, repository.ToPgUUID(transactionID))
	if err != nil {
		return fmt.Errorf("get current transaction state: %w", err)
	}

	if normalizeState(currentState) == normalizeState(nextState) {
		return nil
	}
	if !canTransition(currentState, nextState) {
		return fmt.Errorf("invalid transaction state transition: %s -> %s", currentState, nextState)
	}

	rows, err := qtx.UpdateTransactionStatus(ctx, repository.UpdateTransactionStatusParams{
		Status: nextState,
		ID:     repository.ToPgUUID(transactionID),
	})
	if err != nil {
		return fmt.Errorf("update transaction state: %w", err)
	}
	if err := requireExactlyOne(rows, "update transaction state"); err != nil {
		return err
	}

	if err := audit.Write(ctx, qtx, "transaction", transactionID, actorID, action, currentState, nextState, metadata); err != nil {
		return err
	}

	return nil
}
