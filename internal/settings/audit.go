package settings

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/example/gmcauditor/internal/store"
)

const (
	AuditAction     = "platform_setting_set"
	AuditTargetType = "platform_setting"
)

type postgresAuditor struct {
	pool *pgxpool.Pool
	repo store.PlatformAdminAuditLogRepo
}

func NewPostgresAuditor(pool *pgxpool.Pool) AuditLogger {
	return &postgresAuditor{pool: pool}
}

func (a *postgresAuditor) LogSettingChange(ctx context.Context, adminUserID *uuid.UUID, key, preview string) error {
	target := AuditTargetType
	tid := key
	meta, err := json.Marshal(map[string]string{"preview": preview})
	if err != nil {
		return fmt.Errorf("settings: marshal audit metadata: %w", err)
	}
	return a.repo.Insert(ctx, a.pool, &store.PlatformAdminAuditLogEntry{
		AdminUserID: adminUserID,
		Action:      AuditAction,
		TargetType:  &target,
		TargetID:    &tid,
		Metadata:    meta,
	})
}
