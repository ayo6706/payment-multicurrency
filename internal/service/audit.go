package service

import (
	"context"
	"fmt"

	"github.com/ayo6706/payment-multicurrency/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// AuditService writes immutable audit trail entries.
type AuditService struct {
	store QueryStore
}

func NewAuditService(store QueryStore) *AuditService {
	return &AuditService{store: store}
}

// Write stores a single immutable audit record.
func (s *AuditService) Write(ctx context.Context, qtx *repository.Queries, entityType string, entityID uuid.UUID, actorID *uuid.UUID, action, prevState, nextState string, metadata []byte) error {
	var actor pgtype.UUID
	if actorID != nil {
		actor = repository.ToPgUUID(*actorID)
	}

	if _, err := qtx.InsertAuditLog(ctx, repository.InsertAuditLogParams{
		EntityType: entityType,
		EntityID:   repository.ToPgUUID(entityID),
		ActorID:    actor,
		Action:     action,
		PrevState:  textParam(prevState),
		NextState:  textParam(nextState),
		Metadata:   metadata,
	}); err != nil {
		return fmt.Errorf("insert audit log: %w", err)
	}
	return nil
}

func textParam(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}
