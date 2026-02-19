package service

import (
	"context"

	"github.com/shopspring/decimal"
)

// ExchangeRateService defines the interface for fetching FX rates.
type ExchangeRateService interface {
	// GetExchangeRate returns the rate to convert from source to target currency.
	GetExchangeRate(ctx context.Context, sourceCurrency, targetCurrency string) (decimal.Decimal, error)
}

// MockExchangeRateService is a static implementation for testing.
type MockExchangeRateService struct{}

func NewMockExchangeRateService() *MockExchangeRateService {
	return &MockExchangeRateService{}
}

// GetExchangeRate returns static/mocked rates.
// USD -> EUR: 0.92
// USD -> GBP: 0.79
// EUR -> USD: 1.087 (1/0.92)
// GBP -> USD: 1.266 (1/0.79)
func (s *MockExchangeRateService) GetExchangeRate(ctx context.Context, source, target string) (decimal.Decimal, error) {
	if source == target {
		return decimal.NewFromInt(1), nil
	}

	// Base rates relative to USD
	rates := map[string]float64{
		"USD": 1.0,
		"EUR": 0.92,
		"GBP": 0.79,
	}

	sourceRate, ok1 := rates[source]
	targetRate, ok2 := rates[target]

	if !ok1 || !ok2 {
		return decimal.Zero, nil // Handle e.g. unknown currency
	}

	// Rate = Target / Source
	// e.g. USD -> EUR = 0.92 / 1.0 = 0.92
	// e.g. EUR -> USD = 1.0 / 0.92 = 1.0869...

	sRate := decimal.NewFromFloat(sourceRate)
	tRate := decimal.NewFromFloat(targetRate)

	return tRate.Div(sRate), nil
}
