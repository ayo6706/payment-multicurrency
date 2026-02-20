package domain

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

func TestMoneyConversions(t *testing.T) {
	t.Run("to_decimal", func(t *testing.T) {
		cases := []struct {
			name     string
			micros   int64
			currency string
			want     string
		}{
			{name: "usd_two_decimals", micros: 10_500_000, currency: "USD", want: "10.5"},
			{name: "zero", micros: 0, currency: "EUR", want: "0"},
		}

		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				got := NewMoney(tc.micros, tc.currency).ToDecimal().String()
				assert.Equal(t, tc.want, got)
			})
		}
	})

	t.Run("from_decimal", func(t *testing.T) {
		cases := []struct {
			name string
			in   decimal.Decimal
			want int64
		}{
			{name: "exact_half", in: decimal.NewFromFloat(10.50), want: 10_500_000},
			{name: "integer", in: decimal.NewFromInt(3), want: 3_000_000},
		}

		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				assert.Equal(t, tc.want, FromDecimal(tc.in))
			})
		}
	})

	t.Run("convert", func(t *testing.T) {
		cases := []struct {
			name           string
			sourceMicros   int64
			sourceCurrency string
			targetCurrency string
			rate           decimal.Decimal
			wantMicros     int64
		}{
			{
				name:           "usd_to_eur",
				sourceMicros:   100_000_000,
				sourceCurrency: "USD",
				targetCurrency: "EUR",
				rate:           decimal.NewFromFloat(0.92),
				wantMicros:     92_000_000,
			},
			{
				name:           "precision",
				sourceMicros:   100_000_000,
				sourceCurrency: "USD",
				targetCurrency: "EUR",
				rate:           decimal.NewFromFloat(0.925555),
				wantMicros:     92_555_500,
			},
		}

		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				source := NewMoney(tc.sourceMicros, tc.sourceCurrency)
				got := source.Convert(tc.targetCurrency, tc.rate)
				assert.Equal(t, tc.targetCurrency, got.Currency)
				assert.Equal(t, tc.wantMicros, got.Amount)
			})
		}
	})
}
