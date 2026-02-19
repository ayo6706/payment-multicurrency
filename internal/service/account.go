package service

import (
	"context"

	"github.com/ayo6706/payment-multicurrency/internal/models"
	"github.com/ayo6706/payment-multicurrency/internal/repository"
	"github.com/google/uuid"
)

type AccountService struct {
	repo *repository.Repository
}

func NewAccountService(repo *repository.Repository) *AccountService {
	return &AccountService{
		repo: repo,
	}
}

func (s *AccountService) GetBalance(ctx context.Context, accountID uuid.UUID) (*models.Account, error) {
	return s.repo.GetAccount(ctx, accountID)
}

func (s *AccountService) GetStatement(ctx context.Context, accountID uuid.UUID, page, pageSize int) ([]models.Entry, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	offset := (page - 1) * pageSize
	return s.repo.GetEntries(ctx, accountID, pageSize, offset)
}

func (s *AccountService) CreateAccount(ctx context.Context, userID uuid.UUID, currency string, balance int64) (*models.Account, error) {
	account := &models.Account{
		ID:       uuid.New(),
		UserID:   userID,
		Currency: currency,
		Balance:  balance,
	}
	if err := s.repo.CreateAccount(ctx, account); err != nil {
		return nil, err
	}
	return account, nil
}
