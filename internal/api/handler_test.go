package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/ayo6706/payment-multicurrency/internal/api"
	"github.com/ayo6706/payment-multicurrency/internal/api/middleware"
	"github.com/ayo6706/payment-multicurrency/internal/models"
	"github.com/ayo6706/payment-multicurrency/internal/repository"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testDB *pgxpool.Pool

func TestMain(m *testing.M) {
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		connStr = "postgres://user:password@localhost:5432/payment_system"
	}

	var err error
	testDB, err = pgxpool.New(context.Background(), connStr)
	if err != nil {
		fmt.Printf("Unable to connect to database: %v\n", err)
		os.Exit(1)
	}
	defer testDB.Close()

	if err := testDB.Ping(context.Background()); err != nil {
		fmt.Printf("Unable to ping database: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()
	os.Exit(code)
}

func cleanupDB(t *testing.T) {
	_, err := testDB.Exec(context.Background(), "TRUNCATE TABLE users, accounts, transactions, entries CASCADE")
	require.NoError(t, err)
}

func setupAPI() *api.Router {
	repo := repository.NewRepository(testDB)
	return api.NewRouter(testDB, repo)
}

func generateTestToken(userID string) string {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id": userID,
		"exp":     time.Now().Add(time.Hour).Unix(),
	})
	tokenString, _ := token.SignedString(middleware.JWTSecret)
	return tokenString
}

func TestCreateUser(t *testing.T) {
	cleanupDB(t)
	a := setupAPI()
	router := a.Routes()

	payload := map[string]string{
		"username": "ayo",
		"email":    "ayo@example.com",
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest("POST", "/v1/users", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	if w.Code == http.StatusCreated {
		var response models.User
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
		assert.NotEmpty(t, response.ID)
		assert.Equal(t, "ayo", response.Username)
	}
}

func TestCreateAccount(t *testing.T) {
	cleanupDB(t)
	a := setupAPI()
	router := a.Routes()
	repo := repository.NewRepository(testDB)

	// 1. Create User directly via Repo
	u := &models.User{
		ID:       uuid.New(),
		Username: "david",
		Email:    "david@example.com",
	}
	err := repo.CreateUser(context.Background(), u)
	require.NoError(t, err)

	// 2. Create Account via API
	accPayload := map[string]interface{}{
		"user_id":  u.ID,
		"currency": "USD",
		"balance":  1000,
	}
	accBody, _ := json.Marshal(accPayload)

	req := httptest.NewRequest("POST", "/v1/accounts", bytes.NewBuffer(accBody))
	req.Header.Set("Authorization", "Bearer "+generateTestToken(u.ID.String()))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	if w.Code == http.StatusCreated {
		var accResp models.Account
		err := json.Unmarshal(w.Body.Bytes(), &accResp)
		require.NoError(t, err)
		assert.Equal(t, u.ID, accResp.UserID)
		assert.Equal(t, int64(1000), accResp.Balance)
	}
}

func TestTransfer_Idempotency(t *testing.T) {
	cleanupDB(t)
	a := setupAPI()
	router := a.Routes()
	repo := repository.NewRepository(testDB)

	// Setup: Create two users and accounts
	u1 := &models.User{ID: uuid.New(), Username: "ayo", Email: "ayo@test.com"}
	require.NoError(t, repo.CreateUser(context.Background(), u1))
	acc1 := &models.Account{ID: uuid.New(), UserID: u1.ID, Currency: "USD", Balance: 100}
	require.NoError(t, repo.CreateAccount(context.Background(), acc1))

	u2 := &models.User{ID: uuid.New(), Username: "david", Email: "david@test.com"}
	require.NoError(t, repo.CreateUser(context.Background(), u2))
	acc2 := &models.Account{ID: uuid.New(), UserID: u2.ID, Currency: "USD", Balance: 0}
	require.NoError(t, repo.CreateAccount(context.Background(), acc2))

	// Transfer Request
	transferPayload := map[string]interface{}{
		"from_account_id": acc1.ID,
		"to_account_id":   acc2.ID,
		"amount":          50,
	}
	body, _ := json.Marshal(transferPayload)
	idempotencyKey := uuid.New().String()

	// 1. First Request
	req1 := httptest.NewRequest("POST", "/v1/transfers/internal", bytes.NewBuffer(body))
	req1.Header.Set("Authorization", "Bearer "+generateTestToken(u1.ID.String()))
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set("Idempotency-Key", idempotencyKey)
	w1 := httptest.NewRecorder()

	router.ServeHTTP(w1, req1)

	assert.Equal(t, http.StatusCreated, w1.Code)

	// 2. Second Request (Retry with same key)
	req2 := httptest.NewRequest("POST", "/v1/transfers/internal", bytes.NewBuffer(body))
	req2.Header.Set("Authorization", "Bearer "+generateTestToken(u1.ID.String()))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Idempotency-Key", idempotencyKey)
	w2 := httptest.NewRecorder()

	router.ServeHTTP(w2, req2)

	// Should still be OK/Created, balance updated ONCE
	assert.Contains(t, []int{http.StatusOK, http.StatusCreated}, w2.Code)

	// Verify Balances
	updatedAcc1, err := repo.GetAccount(context.Background(), acc1.ID)
	require.NoError(t, err)
	updatedAcc2, err := repo.GetAccount(context.Background(), acc2.ID)
	require.NoError(t, err)

	assert.Equal(t, int64(50), updatedAcc1.Balance)
	assert.Equal(t, int64(50), updatedAcc2.Balance)
}
