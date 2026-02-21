package service

import (
	"context"
	"fmt"

	"github.com/ayo6706/payment-multicurrency/internal/observability"
	"go.uber.org/zap"
)

// ReconciliationService verifies ledger integrity invariants.
type ReconciliationService struct {
	store QueryStore
}

// NewReconciliationService creates a reconciliation service.
func NewReconciliationService(store QueryStore) *ReconciliationService {
	return &ReconciliationService{store: store}
}

// Run checks that the net sum of all ledger entries is zero.
func (s *ReconciliationService) Run(ctx context.Context) error {
	queries := s.store.Queries()
	net, err := queries.GetLedgerNet(ctx)
	if err != nil {
		return fmt.Errorf("run ledger net query: %w", err)
	}

	if net != 0 {
		observability.IncrementLedgerImbalance("ALL")
		zap.L().Error("CRITICAL: ledger imbalance detected", zap.Int64("net_amount", net))

		imbalances, byCurrencyErr := queries.GetLedgerCurrencyImbalances(ctx)
		if byCurrencyErr == nil {
			for _, row := range imbalances {
				observability.IncrementLedgerImbalance(row.Currency)
				zap.L().Error("ledger imbalance by currency", zap.String("currency", row.Currency), zap.Int64("net_amount", row.NetAmount))
			}
		} else {
			zap.L().Error("failed to load currency imbalances", zap.Error(byCurrencyErr))
		}
		return nil
	}

	zap.L().Info("Ledger Balanced")
	return nil
}
