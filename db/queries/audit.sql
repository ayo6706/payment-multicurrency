-- name: InsertAuditLog :one
INSERT INTO audit_log (
  entity_type,
  entity_id,
  actor_id,
  action,
  prev_state,
  next_state,
  metadata,
  created_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
RETURNING id;

-- name: GetAuditLogsByEntity :many
SELECT id, entity_type, entity_id, actor_id, action, prev_state, next_state, metadata, created_at
FROM audit_log
WHERE entity_type = $1 AND entity_id = $2
ORDER BY id ASC;
