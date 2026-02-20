package gateway

import (
	"context"
	"fmt"
	"math/rand"
	"time"
)

// Gateway represents the external payment gateway interface.
type Gateway interface {
	// SendPayout sends a payout to an external destination.
	// Returns a gateway reference ID and an error if the payout failed.
	SendPayout(ctx context.Context, destination string, amount int64, currency string) (string, error)
}

// MockGateway simulates an external payment gateway for testing.
// It introduces a random delay (2-5 seconds) and fails ~10% of the time.
type MockGateway struct {
	// FailureRate is the probability of failure (0.0 to 1.0). Default: 0.1 (10%)
	FailureRate float64
}

// NewMockGateway creates a new MockGateway with default settings.
func NewMockGateway() *MockGateway {
	return &MockGateway{
		FailureRate: 0.1, // 10% failure rate
	}
}

// SendPayout simulates sending a payout to an external gateway.
// It sleeps for 2-5 seconds to simulate network latency, then randomly
// fails based on the FailureRate. Returns a fake reference ID on success.
func (g *MockGateway) SendPayout(ctx context.Context, destination string, amount int64, currency string) (string, error) {
	// Simulate network delay: 2-5 seconds
	delay := 2 + rand.Intn(3) // 2, 3, or 4 seconds, plus random ms
	delayMs := time.Duration(delay*1000+rand.Intn(1000)) * time.Millisecond

	select {
	case <-time.After(delayMs):
		// Continue after delay
	case <-ctx.Done():
		return "", fmt.Errorf("gateway call canceled: %w", ctx.Err())
	}

	// Randomly fail based on FailureRate
	if rand.Float64() < g.FailureRate {
		return "", fmt.Errorf("gateway temporarily unavailable")
	}

	// Generate fake reference ID
	// Format: MOCK-YYYYMMDD-HHMMSS-XXXXX
	ref := fmt.Sprintf("MOCK-%s-%05d", time.Now().Format("20060102-150405"), rand.Intn(100000))
	return ref, nil
}
