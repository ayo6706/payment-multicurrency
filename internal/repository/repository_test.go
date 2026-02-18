package repository

import (
	"context"
	"os"
	"testing"

	"github.com/ayo6706/payment-multicurrency/internal/db"
	"github.com/ayo6706/payment-multicurrency/internal/models"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
)

func init() {
	_ = godotenv.Load("../../.env") // Load from root
}

func TestCreateUser_And_Account(t *testing.T) {
	// Check if DB url is present (loaded from .env or system)
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("Skipping integration test: DATABASE_URL not set")
	}

	pool, err := db.Connect()
	if err != nil {
		t.Fatalf("Failed to connect to DB: %v", err)
	}
	defer pool.Close()

	repo := NewRepository(pool)
	ctx := context.Background()

	// 1. Test CreateUser
	userID := uuid.New()
	user := &models.User{
		ID:       userID,
		Username: "testuser_" + userID.String()[:8],
		Email:    "test_" + userID.String()[:8] + "@example.com",
	}

	err = repo.CreateUser(ctx, user)
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	// Verify
	var count int
	err = pool.QueryRow(ctx, "SELECT count(*) FROM users WHERE id=$1", user.ID).Scan(&count)
	if err != nil {
		t.Fatalf("Failed to query user: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected 1 user, got %d", count)
	}

	// 2. Test CreateAccount
	accountID := uuid.New()
	account := &models.Account{
		ID:       accountID,
		UserID:   user.ID,
		Currency: "USD",
		Balance:  0, // Initial balance
	}

	err = repo.CreateAccount(ctx, account)
	if err != nil {
		t.Fatalf("CreateAccount failed: %v", err)
	}

	// Verify Account creation logic
	err = pool.QueryRow(ctx, "SELECT count(*) FROM accounts WHERE id=$1", account.ID).Scan(&count)
	if err != nil {
		t.Fatalf("Failed to query account: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected 1 account, got %d", count)
	}

	// Verify balance is 0
	var balance int64
	err = pool.QueryRow(ctx, "SELECT balance FROM accounts WHERE id=$1", account.ID).Scan(&balance)
	if err != nil {
		t.Fatalf("Failed to query account balance: %v", err)
	}
	if balance != 0 {
		t.Errorf("Expected balance 0, got %d", balance)
	}
}
