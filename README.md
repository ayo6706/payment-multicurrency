# Payment Processing System

A robust, concurrent, and idempotent payment processing system written in Go.

## Features

- **User Management**: Registration and user profiles.
- **Multi-Currency Accounts**: Create accounts in different currencies (e.g., USD, EUR).
- **Double-Entry Ledger**: profound tracking of all financial movements (credits and debits).
- **Internal Transfers**: Secure, atomic, and idempotent money transfers between accounts.
- **Authentication**: JWT-based authentication for securing endpoints.
- **RESTful API**: Clean and consistent API design.

## Architecture

This project follows a clean architecture pattern:
- **Handler Layer (`internal/api/handler`)**: Handles HTTP requests, validation, and response formatting.
- **Service Layer (`internal/service`)**: Contains business logic (e.g., transfer rules, account management).
- **Repository Layer (`internal/repository`)**: Direct database interactions using `pgx`.
- **Database**: PostgreSQL with a strict schema ensuring data integrity.

### Key Design Decisions
- **Pessimistic Locking**: `SELECT ... FOR UPDATE` is used during transfers to prevent race conditions and ensure data consistency.
- **Idempotency**: The `Idempotency-Key` header prevents duplicate transaction processing.
- **Double-Entry**: Every transaction records two entries (debit and credit), ensuring the system is auditable and balanced.
- **Test-Driven Development (TDD)**: Business logic, particularly the core transfer and double-entry ledger mechanics, was implemented using TDD. Integration tests simulating deadlocks and concurrent transfers were written first to ensure absolute safety before implementation.

## Getting Started

### Prerequisites

- Go 1.21+
- Docker & Docker Compose

### Setup

1. **Clone the repository**
   ```bash
   git clone <repository-url>
   cd payment-multicurrency
   ```

2. **Start the Database**
   ```bash
   docker-compose up -d
   ```

3. **Run Migrations**
   You can run migrations using `golang-migrate` (or similar tooling) against the `db/migrations` folder:
   ```bash
   migrate -path db/migrations -database "postgresql://user:password@localhost:5432/payment_system?sslmode=disable" up
   ```

4. **Run the Server**
   ```bash
   go run cmd/api/main.go
   ```
   The server will start on port `8080`.

## API Usage

### 1. Create User
```bash
curl -X POST http://localhost:8080/v1/users \
  -H "Content-Type: application/json" \
  -d '{"username":"ayo", "email":"ayo@example.com"}'
```

### 2. Login (Get Token)
```bash
curl -X POST http://localhost:8080/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"user_id":"<USER_UUID>"}'
```

### 3. Create Account
```bash
curl -X POST http://localhost:8080/v1/accounts \
  -H "Authorization: Bearer <TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{"user_id":"<USER_UUID>", "currency":"USD", "balance":1000}'
```

### 4. Make Internal Transfer
```bash
curl -X POST http://localhost:8080/v1/transfers/internal \
  -H "Authorization: Bearer <TOKEN>" \
  -H "Idempotency-Key: <UNIQUE_KEY>" \
  -H "Content-Type: application/json" \
  -d '{"from_account_id":"<ACC_ID_1>", "to_account_id":"<ACC_ID_2>", "amount":100000000}'
```

### 5. Request External Payout
```bash
curl -X POST http://localhost:8080/v1/payouts \
  -H "Authorization: Bearer <TOKEN>" \
  -H "Idempotency-Key: <UNIQUE_KEY>" \
  -H "Content-Type: application/json" \
  -d '{
    "account_id":"<ACC_ID>",
    "amount_micros":50000000,
    "currency":"GBP",
    "destination":{"iban":"GB29NWBK60161331926819", "name":"John Doe"}
  }'
```

### 6. Get Statement
```bash
curl -X GET "http://localhost:8080/v1/accounts/<ACC_ID>/statement?page=1&page_size=10" \
  -H "Authorization: Bearer <TOKEN>"
```

## Testing

Run the integration tests:
```bash
go test -v ./internal/api/...
```
