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

func TestCreateUserAndAccount(t *testing.T) {
	// Check if DB url is present (loaded from .env or system)
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("Skipping integration test: DATABASE_URL not set")
	}

	pool, err := db.Connect(context.Background(), os.Getenv("DATABASE_URL"))
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

	// Verify User creation logic using GetUser method
	dbUser, err := repo.GetUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("Failed to get user: %v", err)
	}
	if dbUser.ID != user.ID {
		t.Errorf("Expected user ID %s, got %s", user.ID, dbUser.ID)
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

	// Verify Account creation logic using GetAccount method
	dbAccount, err := repo.GetAccount(ctx, account.ID)
	if err != nil {
		t.Fatalf("Failed to get account: %v", err)
	}
	if dbAccount.ID != account.ID {
		t.Errorf("Expected account ID %s, got %s", account.ID, dbAccount.ID)
	}

	// Verify balance is 0
	if dbAccount.Balance != 0 {
		t.Errorf("Expected balance 0, got %d", dbAccount.Balance)
	}
}
