package domain

import (
	"fmt"

	"github.com/shopspring/decimal"
)

// Money represents a monetary value in a specific currency.
// Amount is stored as BIGINT micros (10^-6) to avoid floating point errors.
type Money struct {
	Amount   int64  // micros
	Currency string // ISO 4217
}

// NewMoney creates a new Money instance from micros.
func NewMoney(amount int64, currency string) Money {
	return Money{
		Amount:   amount,
		Currency: currency,
	}
}

// ToDecimal converts the int64 micros to a shopspring/decimal.Decimal.
func (m Money) ToDecimal() decimal.Decimal {
	return decimal.NewFromInt(m.Amount).Div(decimal.NewFromInt(1_000_000))
}

// FromDecimal converts a decimal.Decimal to int64 micros.
func FromDecimal(d decimal.Decimal) int64 {
	return d.Mul(decimal.NewFromInt(1_000_000)).IntPart()
}

// Multiply returns a new Money instance multiplied by a factor (e.g. FX rate).
// It uses shopspring/decimal for precision and rounds down.
func (m Money) Multiply(factor decimal.Decimal) Money {
	amountDec := m.ToDecimal().Mul(factor)
	return Money{
		Amount:   FromDecimal(amountDec),
		Currency: m.Currency, // Note: Currency usually changes in FX, but this just scales amount
	}
}

// ConvertCurrency converts the money to a target currency using a given FX rate.
// The rate should be (Target / Source).
func (m Money) Convert(targetCurrency string, rate decimal.Decimal) Money {
	amountDec := m.ToDecimal().Mul(rate)
	return Money{
		Amount:   FromDecimal(amountDec),
		Currency: targetCurrency,
	}
}

// String returns the string representation of the money.
func (m Money) String() string {
	return fmt.Sprintf("%s %s", m.ToDecimal().StringFixed(2), m.Currency)
}
