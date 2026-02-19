package domain

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

func TestMoney_ToDecimal(t *testing.T) {
	m := NewMoney(10_500_000, "USD") // 10.50 USD
	d := m.ToDecimal()
	assert.Equal(t, "10.5", d.String())
}

func TestFromDecimal(t *testing.T) {
	d := decimal.NewFromFloat(10.50)
	micros := FromDecimal(d)
	assert.Equal(t, int64(10_500_000), micros)
}

func TestMoney_Convert(t *testing.T) {
	// Source: 100 USD
	source := NewMoney(100_000_000, "USD")

	// Rate: 1 USD = 0.92 EUR
	rate := decimal.NewFromFloat(0.92)

	// Target: 92 EUR
	target := source.Convert("EUR", rate)

	assert.Equal(t, "EUR", target.Currency)
	assert.Equal(t, int64(92_000_000), target.Amount)
}

func TestMoney_Convert_Precision(t *testing.T) {
	// Source: 100 USD
	source := NewMoney(100_000_000, "USD")

	// Rate: 1 USD = 0.925555 EUR
	// Expected: 92.5555 EUR -> 92,555,500 micros
	rate := decimal.NewFromFloat(0.925555)

	target := source.Convert("EUR", rate)

	assert.Equal(t, int64(92_555_500), target.Amount)
}
