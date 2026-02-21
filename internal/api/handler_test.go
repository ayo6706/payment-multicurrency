package api_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/ayo6706/payment-multicurrency/internal/api"
	"github.com/ayo6706/payment-multicurrency/internal/api/middleware"
	"github.com/ayo6706/payment-multicurrency/internal/config"
	"github.com/ayo6706/payment-multicurrency/internal/domain"
	"github.com/ayo6706/payment-multicurrency/internal/gateway"
	"github.com/ayo6706/payment-multicurrency/internal/idempotency"
	"github.com/ayo6706/payment-multicurrency/internal/models"
	"github.com/ayo6706/payment-multicurrency/internal/repository"
	"github.com/ayo6706/payment-multicurrency/internal/service"
	"github.com/ayo6706/payment-multicurrency/internal/testutil/dblock"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

var testDB *pgxpool.Pool

const (
	testJWTSecret   = "test-secret-0123456789-test-secret"
	testJWTIssuer   = "payment-multicurrency-test"
	testJWTAudience = "payment-api-test"
)

func TestMain(m *testing.M) {
	release := dblock.Acquire()
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		connStr = "postgres://user:password@localhost:5432/payment_system"
	}

	var err error
	testDB, err = pgxpool.New(context.Background(), connStr)
	if err != nil {
		release()
		fmt.Printf("Unable to connect to database: %v\n", err)
		os.Exit(1)
	}
	defer testDB.Close()

	ctx := context.Background()
	if err := testDB.Ping(ctx); err != nil {
		release()
		fmt.Printf("Unable to ping database: %v\n", err)
		os.Exit(1)
	}

	ensureIdempotencyTable(ctx)
	ensureAuditLogTable(ctx)
	middleware.SetJWTSecret(testJWTSecret)
	middleware.SetJWTValidation(testJWTIssuer, testJWTAudience)

	code := m.Run()
	release()
	os.Exit(code)
}

func cleanupDB(t *testing.T) {
	_, err := testDB.Exec(context.Background(), "TRUNCATE TABLE audit_log, payouts, users, accounts, transactions, entries, idempotency_keys CASCADE")
	require.NoError(t, err)
	seedSystemAccounts(t)
}

func setupAPI() *api.Router {
	repo := repository.NewRepository(testDB)
	store := repository.NewStore(testDB)
	accountSvc := service.NewAccountService(repo)
	transferSvc := service.NewTransferService(store, service.NewMockExchangeRateService())
	payoutSvc := service.NewPayoutService(store, gateway.NewMockGateway())
	webhookSvc := service.NewWebhookService(store, "test", false)
	cfg := &config.Config{
		HTTPPort:             "0",
		JWTSecret:            testJWTSecret,
		JWTIssuer:            testJWTIssuer,
		JWTAudience:          testJWTAudience,
		WebhookHMACKey:       "test",
		WebhookSkipSignature: false,
		PublicRateLimitRPS:   1000,
		AuthRateLimitRPS:     1000,
		PayoutPollInterval:   time.Second,
		PayoutBatchSize:      5,
		IdempotencyTTL:       time.Hour,
	}
	idemStore := idempotency.NewStore(nil, testDB, cfg.IdempotencyTTL)
	return api.NewRouter(cfg, zap.NewNop(), testDB, repo, idemStore, nil, accountSvc, transferSvc, payoutSvc, webhookSvc)
}

func generateTestToken(userID string) string {
	return generateTokenWithRole(userID, "user")
}

func generateTokenWithRole(userID, role string) string {
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id": userID,
		"role":    role,
		"iss":     testJWTIssuer,
		"aud":     testJWTAudience,
		"sub":     userID,
		"iat":     now.Unix(),
		"nbf":     now.Add(-30 * time.Second).Unix(),
		"exp":     now.Add(time.Hour).Unix(),
	})
	tokenString, _ := token.SignedString(middleware.JWTSecret())
	return tokenString
}

func ensureIdempotencyTable(ctx context.Context) {
	ddl := `
CREATE TABLE IF NOT EXISTS idempotency_keys (
	    idempotency_key TEXT PRIMARY KEY,
	    request_hash TEXT NOT NULL,
	    method TEXT NOT NULL,
	    path TEXT NOT NULL,
	    response_status INTEGER NOT NULL DEFAULT 0,
	    response_body BYTEA NOT NULL DEFAULT ''::bytea,
	    content_type TEXT NOT NULL DEFAULT 'application/json',
	    in_progress BOOLEAN NOT NULL DEFAULT TRUE,
	    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
`
	if _, err := testDB.Exec(ctx, ddl); err != nil {
		fmt.Printf("failed to ensure idempotency table: %v\n", err)
		os.Exit(1)
	}
	alter := `
ALTER TABLE idempotency_keys
	ADD COLUMN IF NOT EXISTS in_progress BOOLEAN NOT NULL DEFAULT TRUE,
	ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
ALTER TABLE idempotency_keys ALTER COLUMN response_status SET DEFAULT 0;
ALTER TABLE idempotency_keys ALTER COLUMN response_body SET DEFAULT ''::bytea;
ALTER TABLE idempotency_keys ALTER COLUMN content_type SET DEFAULT 'application/json';
`
	if _, err := testDB.Exec(ctx, alter); err != nil {
		fmt.Printf("failed to alter idempotency table: %v\n", err)
		os.Exit(1)
	}
}

func ensureAuditLogTable(ctx context.Context) {
	ddl := `
CREATE TABLE IF NOT EXISTS audit_log (
	    id BIGSERIAL PRIMARY KEY,
	    entity_type TEXT NOT NULL,
	    entity_id UUID NOT NULL,
	    actor_id UUID,
	    action TEXT NOT NULL,
	    prev_state TEXT,
	    next_state TEXT,
	    metadata JSONB,
	    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
`
	if _, err := testDB.Exec(ctx, ddl); err != nil {
		fmt.Printf("failed to ensure audit_log table: %v\n", err)
		os.Exit(1)
	}
}

func TestRFC7807ProblemDetails(t *testing.T) {
	cleanupDB(t)
	a := setupAPI()
	client := a.Routes()

	accountID := uuid.New().String()
	req := httptest.NewRequest("GET", "/v1/accounts/"+accountID+"/balance", nil)
	w := httptest.NewRecorder()
	client.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Contains(t, w.Header().Get("Content-Type"), "application/problem+json")

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.NotEmpty(t, body["type"])
	assert.Equal(t, float64(http.StatusUnauthorized), body["status"])
	assert.NotEmpty(t, body["title"])
	assert.NotEmpty(t, body["detail"])
	assert.Equal(t, "/v1/accounts/"+accountID+"/balance", body["instance"])
	assert.NotEmpty(t, body["request_id"])
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

func TestCreateUserIgnoresRequestedRole(t *testing.T) {
	cleanupDB(t)
	a := setupAPI()
	router := a.Routes()

	payload := map[string]string{
		"username": "eve",
		"email":    "eve@example.com",
		"role":     "admin",
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest("POST", "/v1/users", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code)

	var response models.User
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	assert.Equal(t, "user", response.Role)

	loginBody, _ := json.Marshal(map[string]string{"user_id": response.ID.String()})
	loginReq := httptest.NewRequest("POST", "/v1/auth/login", bytes.NewBuffer(loginBody))
	loginReq.Header.Set("Content-Type", "application/json")
	loginW := httptest.NewRecorder()
	router.ServeHTTP(loginW, loginReq)
	require.Equal(t, http.StatusOK, loginW.Code)

	var loginResp struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.Unmarshal(loginW.Body.Bytes(), &loginResp))
	parsed, err := jwt.Parse(loginResp.Token, func(token *jwt.Token) (interface{}, error) {
		return middleware.JWTSecret(), nil
	}, jwt.WithIssuer(testJWTIssuer), jwt.WithAudience(testJWTAudience))
	require.NoError(t, err)
	claims, ok := parsed.Claims.(jwt.MapClaims)
	require.True(t, ok)
	assert.Equal(t, "user", claims["role"])
}

func TestAuthLoginInvalidUser(t *testing.T) {
	cleanupDB(t)
	a := setupAPI()
	client := a.Routes()

	cases := []struct {
		name string
		body map[string]string
		want int
	}{
		{name: "unknown_user", body: map[string]string{"user_id": uuid.New().String()}, want: http.StatusNotFound},
		{name: "invalid_user_id_format", body: map[string]string{"user_id": "not-a-uuid"}, want: http.StatusBadRequest},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(tc.body)
			req := httptest.NewRequest("POST", "/v1/auth/login", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			client.ServeHTTP(w, req)
			assert.Equal(t, tc.want, w.Code)
		})
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

func TestCreateAccountInvalidCurrency(t *testing.T) {
	cleanupDB(t)
	a := setupAPI()
	client := a.Routes()
	repo := repository.NewRepository(testDB)

	user := &models.User{ID: uuid.New(), Username: "acct", Email: "acct@example.com"}
	require.NoError(t, repo.CreateUser(context.Background(), user))

	token := generateTestToken(user.ID.String())
	reqPayload := map[string]interface{}{
		"user_id":  user.ID,
		"currency": "XYZ",
		"balance":  0,
	}
	body, _ := json.Marshal(reqPayload)
	req := httptest.NewRequest("POST", "/v1/accounts", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	client.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetBalanceSuccessAndUnauthorized(t *testing.T) {
	cleanupDB(t)
	a := setupAPI()
	client := a.Routes()
	repo := repository.NewRepository(testDB)

	user := &models.User{ID: uuid.New(), Username: "bal", Email: "bal@example.com"}
	require.NoError(t, repo.CreateUser(context.Background(), user))
	acct := &models.Account{ID: uuid.New(), UserID: user.ID, Currency: "USD", Balance: 42}
	require.NoError(t, repo.CreateAccount(context.Background(), acct))

	cases := []struct {
		name   string
		token  string
		status int
	}{
		{name: "unauthorized", token: "", status: http.StatusUnauthorized},
		{name: "authorized", token: generateTestToken(user.ID.String()), status: http.StatusOK},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/v1/accounts/"+acct.ID.String()+"/balance", nil)
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			w := httptest.NewRecorder()
			client.ServeHTTP(w, req)
			assert.Equal(t, tc.status, w.Code)
		})
	}
}

func TestGetBalanceForbiddenForNonOwner(t *testing.T) {
	cleanupDB(t)
	a := setupAPI()
	client := a.Routes()
	repo := repository.NewRepository(testDB)

	owner := &models.User{ID: uuid.New(), Username: "owner", Email: "owner@example.com"}
	require.NoError(t, repo.CreateUser(context.Background(), owner))
	other := &models.User{ID: uuid.New(), Username: "other", Email: "other@example.com"}
	require.NoError(t, repo.CreateUser(context.Background(), other))

	acct := &models.Account{ID: uuid.New(), UserID: owner.ID, Currency: "USD", Balance: 42}
	require.NoError(t, repo.CreateAccount(context.Background(), acct))

	req := httptest.NewRequest("GET", "/v1/accounts/"+acct.ID.String()+"/balance", nil)
	req.Header.Set("Authorization", "Bearer "+generateTestToken(other.ID.String()))
	w := httptest.NewRecorder()
	client.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestGetStatementPagination(t *testing.T) {
	cleanupDB(t)
	a := setupAPI()
	client := a.Routes()
	repo := repository.NewRepository(testDB)

	user := &models.User{ID: uuid.New(), Username: "stmt", Email: "stmt@example.com"}
	require.NoError(t, repo.CreateUser(context.Background(), user))
	acct := &models.Account{ID: uuid.New(), UserID: user.ID, Currency: "USD", Balance: 0}
	require.NoError(t, repo.CreateAccount(context.Background(), acct))

	// Insert fake entries
	queries := repository.New(testDB)
	for i := 0; i < 3; i++ {
		transactionID := uuid.New()
		_, err := queries.CreateTransaction(context.Background(), repository.CreateTransactionParams{
			ID:          repository.ToPgUUID(transactionID),
			Amount:      int64(100 * (i + 1)),
			Currency:    "USD",
			Type:        domain.TxTypeDeposit,
			Status:      domain.TxStatusCompleted,
			ReferenceID: fmt.Sprintf("stmt-%d", i),
		})
		require.NoError(t, err)

		entry := repository.CreateEntryParams{
			ID:            repository.ToPgUUID(uuid.New()),
			TransactionID: repository.ToPgUUID(transactionID),
			AccountID:     repository.ToPgUUID(acct.ID),
			Amount:        int64(100 * (i + 1)),
			Direction:     domain.DirectionCredit,
		}
		_, err = queries.CreateEntry(context.Background(), entry)
		require.NoError(t, err)
	}

	req := httptest.NewRequest("GET", "/v1/accounts/"+acct.ID.String()+"/statement?page=1&page_size=2", nil)
	req.Header.Set("Authorization", "Bearer "+generateTestToken(user.ID.String()))
	w := httptest.NewRecorder()
	client.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestTransferInternalInsufficientFunds(t *testing.T) {
	cleanupDB(t)
	a := setupAPI()
	client := a.Routes()
	repo := repository.NewRepository(testDB)

	u1 := &models.User{ID: uuid.New(), Username: "src", Email: "src@example.com"}
	require.NoError(t, repo.CreateUser(context.Background(), u1))
	u2 := &models.User{ID: uuid.New(), Username: "dest", Email: "dest@example.com"}
	require.NoError(t, repo.CreateUser(context.Background(), u2))
	acc1 := &models.Account{ID: uuid.New(), UserID: u1.ID, Currency: "USD", Balance: 10}
	require.NoError(t, repo.CreateAccount(context.Background(), acc1))
	acc2 := &models.Account{ID: uuid.New(), UserID: u2.ID, Currency: "USD", Balance: 0}
	require.NoError(t, repo.CreateAccount(context.Background(), acc2))

	reqBody := map[string]interface{}{
		"from_account_id": acc1.ID,
		"to_account_id":   acc2.ID,
		"amount":          1000,
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/transfers/internal", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+generateTestToken(u1.ID.String()))
	req.Header.Set("Idempotency-Key", uuid.New().String())
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	client.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestTransferInternalForbiddenForNonOwner(t *testing.T) {
	cleanupDB(t)
	a := setupAPI()
	client := a.Routes()
	repo := repository.NewRepository(testDB)

	owner := &models.User{ID: uuid.New(), Username: "owner-src", Email: "owner-src@example.com"}
	require.NoError(t, repo.CreateUser(context.Background(), owner))
	receiver := &models.User{ID: uuid.New(), Username: "owner-dst", Email: "owner-dst@example.com"}
	require.NoError(t, repo.CreateUser(context.Background(), receiver))
	attacker := &models.User{ID: uuid.New(), Username: "attacker", Email: "attacker@example.com"}
	require.NoError(t, repo.CreateUser(context.Background(), attacker))

	src := &models.Account{ID: uuid.New(), UserID: owner.ID, Currency: "USD", Balance: 1_000}
	require.NoError(t, repo.CreateAccount(context.Background(), src))
	dst := &models.Account{ID: uuid.New(), UserID: receiver.ID, Currency: "USD", Balance: 0}
	require.NoError(t, repo.CreateAccount(context.Background(), dst))

	reqBody := map[string]interface{}{
		"from_account_id": src.ID,
		"to_account_id":   dst.ID,
		"amount":          100,
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/transfers/internal", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+generateTestToken(attacker.ID.String()))
	req.Header.Set("Idempotency-Key", uuid.New().String())
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	client.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)

	srcAfter, err := repo.GetAccount(context.Background(), src.ID)
	require.NoError(t, err)
	dstAfter, err := repo.GetAccount(context.Background(), dst.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1_000), srcAfter.Balance)
	assert.Equal(t, int64(0), dstAfter.Balance)
}

func TestTransferExchangeSuccessAndSameAccount(t *testing.T) {
	cleanupDB(t)
	a := setupAPI()
	client := a.Routes()
	repo := repository.NewRepository(testDB)

	u1 := &models.User{ID: uuid.New(), Username: "fx1", Email: "fx1@example.com"}
	require.NoError(t, repo.CreateUser(context.Background(), u1))
	u2 := &models.User{ID: uuid.New(), Username: "fx2", Email: "fx2@example.com"}
	require.NoError(t, repo.CreateUser(context.Background(), u2))
	accUSD := &models.Account{ID: uuid.New(), UserID: u1.ID, Currency: "USD", Balance: 2_000_000}
	require.NoError(t, repo.CreateAccount(context.Background(), accUSD))
	accEUR := &models.Account{ID: uuid.New(), UserID: u2.ID, Currency: "EUR", Balance: 0}
	require.NoError(t, repo.CreateAccount(context.Background(), accEUR))

	cases := []struct {
		name   string
		body   map[string]interface{}
		status int
	}{
		{
			name: "success",
			body: map[string]interface{}{
				"from_account_id": accUSD.ID,
				"to_account_id":   accEUR.ID,
				"amount":          1_000_000,
				"from_currency":   "USD",
				"to_currency":     "EUR",
			},
			status: http.StatusCreated,
		},
		{
			name: "same_account_fails",
			body: map[string]interface{}{
				"from_account_id": accUSD.ID,
				"to_account_id":   accUSD.ID,
				"amount":          1000,
				"from_currency":   "USD",
				"to_currency":     "EUR",
			},
			status: http.StatusBadRequest,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(tc.body)
			req := httptest.NewRequest("POST", "/v1/transfers/exchange", bytes.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+generateTestToken(u1.ID.String()))
			req.Header.Set("Idempotency-Key", uuid.New().String())
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			client.ServeHTTP(w, req)
			assert.Equal(t, tc.status, w.Code)
		})
	}
}

func TestPayoutCreationAuthorizationAndSuccess(t *testing.T) {
	cleanupDB(t)
	a := setupAPI()
	client := a.Routes()
	repo := repository.NewRepository(testDB)

	cases := []struct {
		name      string
		username  string
		email     string
		makeAdmin bool
		status    int
	}{
		{name: "non_admin_forbidden", username: "payout", email: "payout@example.com", makeAdmin: false, status: http.StatusForbidden},
		{name: "admin_accepted", username: "admin", email: "admin@example.com", makeAdmin: true, status: http.StatusAccepted},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			u := &models.User{ID: uuid.New(), Username: tc.username, Email: tc.email}
			require.NoError(t, repo.CreateUser(context.Background(), u))
			if tc.makeAdmin {
				_, err := testDB.Exec(context.Background(), "UPDATE users SET role='admin' WHERE id=$1", repository.ToPgUUID(u.ID))
				require.NoError(t, err)
			}

			acc := &models.Account{ID: uuid.New(), UserID: u.ID, Currency: "USD", Balance: 1_000_000}
			require.NoError(t, repo.CreateAccount(context.Background(), acc))

			reqBody := map[string]interface{}{
				"account_id":    acc.ID,
				"amount_micros": 1000,
				"currency":      "USD",
				"destination": map[string]string{
					"iban": "GB29NWBK60161331926819",
					"name": "Test",
				},
			}
			body, _ := json.Marshal(reqBody)
			req := httptest.NewRequest("POST", "/v1/payouts", bytes.NewReader(body))
			token := loginAndGetToken(t, client, u.ID)
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Idempotency-Key", uuid.New().String())
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			client.ServeHTTP(w, req)
			assert.Equal(t, tc.status, w.Code)
		})
	}
}

func TestGetPayoutUnauthorizedAndNotFound(t *testing.T) {
	cleanupDB(t)
	a := setupAPI()
	client := a.Routes()

	cases := []struct {
		name   string
		token  string
		status int
	}{
		{name: "unauthorized", token: "", status: http.StatusUnauthorized},
		{name: "authorized_not_found", token: generateTestToken(uuid.New().String()), status: http.StatusNotFound},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/v1/payouts/"+uuid.New().String(), nil)
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			w := httptest.NewRecorder()
			client.ServeHTTP(w, req)
			assert.Equal(t, tc.status, w.Code)
		})
	}
}

func TestGetPayoutForbiddenForNonOwner(t *testing.T) {
	cleanupDB(t)
	a := setupAPI()
	client := a.Routes()
	repo := repository.NewRepository(testDB)
	queries := repository.New(testDB)

	owner := &models.User{ID: uuid.New(), Username: "p-owner", Email: "p-owner@example.com"}
	require.NoError(t, repo.CreateUser(context.Background(), owner))
	other := &models.User{ID: uuid.New(), Username: "p-other", Email: "p-other@example.com"}
	require.NoError(t, repo.CreateUser(context.Background(), other))

	acc := &models.Account{ID: uuid.New(), UserID: owner.ID, Currency: "USD", Balance: 1_000_000}
	require.NoError(t, repo.CreateAccount(context.Background(), acc))

	txID := uuid.New()
	_, err := queries.CreateTransaction(context.Background(), repository.CreateTransactionParams{
		ID:          repository.ToPgUUID(txID),
		Amount:      1000,
		Currency:    "USD",
		Type:        domain.TxTypePayout,
		Status:      domain.TxStatusPending,
		ReferenceID: "payout-owner-check",
	})
	require.NoError(t, err)

	payoutID := uuid.New()
	_, err = queries.InsertPayout(context.Background(), repository.InsertPayoutParams{
		ID:            repository.ToPgUUID(payoutID),
		TransactionID: repository.ToPgUUID(txID),
		AccountID:     repository.ToPgUUID(acc.ID),
		AmountMicros:  1000,
		Currency:      "USD",
		Status:        domain.PayoutStatusPending,
	})
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/v1/payouts/"+payoutID.String(), nil)
	req.Header.Set("Authorization", "Bearer "+generateTestToken(other.ID.String()))
	w := httptest.NewRecorder()
	client.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestManualReviewPayoutEndpoints(t *testing.T) {
	cleanupDB(t)
	a := setupAPI()
	client := a.Routes()
	repo := repository.NewRepository(testDB)
	queries := repository.New(testDB)

	admin := &models.User{ID: uuid.New(), Username: "mr-admin", Email: "mr-admin@example.com"}
	require.NoError(t, repo.CreateUser(context.Background(), admin))
	_, err := testDB.Exec(context.Background(), "UPDATE users SET role='admin' WHERE id=$1", repository.ToPgUUID(admin.ID))
	require.NoError(t, err)

	account := &models.Account{ID: uuid.New(), UserID: admin.ID, Currency: "USD", Balance: 1_000_000}
	require.NoError(t, repo.CreateAccount(context.Background(), account))

	txID := uuid.New()
	_, err = queries.CreateTransaction(context.Background(), repository.CreateTransactionParams{
		ID:          repository.ToPgUUID(txID),
		Amount:      1_200,
		Currency:    "USD",
		Type:        domain.TxTypePayout,
		Status:      domain.TxStatusProcessing,
		ReferenceID: "manual-review-" + uuid.NewString(),
	})
	require.NoError(t, err)

	payoutID := uuid.New()
	gatewayRef := "GW-MANUAL-123"
	_, err = queries.InsertPayout(context.Background(), repository.InsertPayoutParams{
		ID:            repository.ToPgUUID(payoutID),
		TransactionID: repository.ToPgUUID(txID),
		AccountID:     repository.ToPgUUID(account.ID),
		AmountMicros:  1_200,
		Currency:      "USD",
		Status:        domain.PayoutStatusManualReview,
	})
	require.NoError(t, err)
	_, err = queries.UpdatePayoutStatus(context.Background(), repository.UpdatePayoutStatusParams{
		Status:     domain.PayoutStatusManualReview,
		GatewayRef: &gatewayRef,
		ID:         repository.ToPgUUID(payoutID),
	})
	require.NoError(t, err)
	_, err = testDB.Exec(context.Background(), "UPDATE accounts SET locked_micros=$1 WHERE id=$2", 1200, repository.ToPgUUID(account.ID))
	require.NoError(t, err)

	token := loginAndGetToken(t, client, admin.ID)

	listReq := httptest.NewRequest("GET", "/v1/payouts/manual-review?limit=10&offset=0", nil)
	listReq.Header.Set("Authorization", "Bearer "+token)
	listW := httptest.NewRecorder()
	client.ServeHTTP(listW, listReq)
	require.Equal(t, http.StatusOK, listW.Code)

	var listResp struct {
		Items []models.Payout `json:"items"`
		Count int             `json:"count"`
	}
	require.NoError(t, json.Unmarshal(listW.Body.Bytes(), &listResp))
	require.GreaterOrEqual(t, listResp.Count, 1)

	resolveBody, _ := json.Marshal(map[string]string{
		"decision": "confirm_sent",
		"reason":   "confirmed with provider report",
	})
	resolveReq := httptest.NewRequest("POST", "/v1/payouts/"+payoutID.String()+"/resolve", bytes.NewReader(resolveBody))
	resolveReq.Header.Set("Authorization", "Bearer "+token)
	resolveReq.Header.Set("Content-Type", "application/json")
	resolveW := httptest.NewRecorder()
	client.ServeHTTP(resolveW, resolveReq)
	require.Equal(t, http.StatusOK, resolveW.Code)

	var resolved models.Payout
	require.NoError(t, json.Unmarshal(resolveW.Body.Bytes(), &resolved))
	require.Equal(t, domain.PayoutStatusCompleted, resolved.Status)

	accountAfter, err := queries.GetAccountBalanceAndLocked(context.Background(), repository.ToPgUUID(account.ID))
	require.NoError(t, err)
	require.Equal(t, int64(998_800), accountAfter.Balance)
	require.Equal(t, int64(0), accountAfter.LockedMicros)
}

func TestWebhookInvalidSignature(t *testing.T) {
	cleanupDB(t)
	a := setupAPI()
	client := a.Routes()

	cases := []struct {
		name      string
		signature string
	}{
		{name: "bad_signature", signature: "bad"},
		{name: "missing_signature", signature: ""},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			payload := []byte(`{"account_id":"` + uuid.New().String() + `","amount_micros":1000,"currency":"USD","reference":"abc"}`)
			req := httptest.NewRequest("POST", "/v1/webhooks/deposit", bytes.NewReader(payload))
			if tc.signature != "" {
				req.Header.Set("X-Webhook-Signature", tc.signature)
			}
			w := httptest.NewRecorder()
			client.ServeHTTP(w, req)
			assert.Equal(t, http.StatusUnauthorized, w.Code)
		})
	}
}

func TestWebhookDepositSuccess(t *testing.T) {
	cleanupDB(t)
	a := setupAPI()
	client := a.Routes()
	repo := repository.NewRepository(testDB)

	user := &models.User{ID: uuid.New(), Username: "hook", Email: "hook@example.com"}
	require.NoError(t, repo.CreateUser(context.Background(), user))
	acct := &models.Account{ID: uuid.New(), UserID: user.ID, Currency: "USD", Balance: 0}
	require.NoError(t, repo.CreateAccount(context.Background(), acct))

	cases := []struct {
		name      string
		reference string
		status    int
	}{
		{name: "first_delivery", reference: "hook-success", status: http.StatusOK},
		{name: "idempotent_redelivery", reference: "hook-success", status: http.StatusOK},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			payload := map[string]interface{}{
				"account_id":    acct.ID,
				"amount_micros": 1000,
				"currency":      "USD",
				"reference":     tc.reference,
			}
			body, _ := json.Marshal(payload)
			sig := computeHMAC(body, "test")
			req := httptest.NewRequest("POST", "/v1/webhooks/deposit", bytes.NewReader(body))
			req.Header.Set("X-Webhook-Signature", sig)
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			client.ServeHTTP(w, req)
			assert.Equal(t, tc.status, w.Code)
		})
	}
}

func TestHealthAndMetrics(t *testing.T) {
	cleanupDB(t)
	a := setupAPI()
	client := a.Routes()

	cases := []struct {
		name string
		path string
	}{
		{name: "live", path: "/health/live"},
		{name: "ready", path: "/health/ready"},
		{name: "metrics", path: "/metrics"},
		{name: "openapi", path: "/openapi.yaml"},
		{name: "swagger", path: "/swagger/index.html"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tc.path, nil)
			w := httptest.NewRecorder()
			client.ServeHTTP(w, req)
			assert.Equal(t, http.StatusOK, w.Code)
		})
	}
}

func computeHMAC(payload []byte, key string) string {
	h := hmac.New(sha256.New, []byte(key))
	h.Write(payload)
	return "sha256=" + hex.EncodeToString(h.Sum(nil))
}

func loginAndGetToken(t *testing.T, handler http.Handler, userID uuid.UUID) string {
	payload := map[string]string{"user_id": userID.String()}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp.Token
}

func seedSystemAccounts(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	_, err := testDB.Exec(ctx, `
		INSERT INTO users (id, username, email, role)
		VALUES
		('11111111-1111-1111-1111-111111111111','system_liquidity','system@grey.finance','system')
		ON CONFLICT (id) DO NOTHING;
	`)
	require.NoError(t, err)
	_, err = testDB.Exec(ctx, `
		INSERT INTO accounts (id, user_id, currency, balance, locked_micros)
		VALUES
		('22222222-2222-2222-2222-222222222222','11111111-1111-1111-1111-111111111111','USD',0,0),
		('33333333-3333-3333-3333-333333333333','11111111-1111-1111-1111-111111111111','EUR',0,0),
		('44444444-4444-4444-4444-444444444444','11111111-1111-1111-1111-111111111111','GBP',0,0)
		ON CONFLICT (id) DO NOTHING;
	`)
	require.NoError(t, err)
}

func TestTransferIdempotency(t *testing.T) {
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
