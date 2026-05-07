package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type GumroadWebhookEvent struct {
	ID             uuid.UUID
	GumroadEventID *string
	EventType      string
	Payload        []byte
	ProcessedAt    *time.Time
	ErrorMessage   *string
	ReceivedAt     time.Time
}

type GumroadWebhookEventsRepo struct{}

func (GumroadWebhookEventsRepo) Insert(ctx context.Context, q Querier, e *GumroadWebhookEvent) error {
	if len(e.Payload) == 0 {
		e.Payload = []byte("{}")
	}
	return translatePgErr(q.QueryRow(ctx, `
		INSERT INTO gumroad_webhook_events (gumroad_event_id, event_type, payload)
		VALUES ($1, $2, $3)
		ON CONFLICT (gumroad_event_id) DO NOTHING
		RETURNING id, received_at
	`, e.GumroadEventID, e.EventType, e.Payload).Scan(&e.ID, &e.ReceivedAt))
}

func (GumroadWebhookEventsRepo) MarkProcessed(ctx context.Context, q Querier, id uuid.UUID, at time.Time) error {
	tag, err := q.Exec(ctx,
		`UPDATE gumroad_webhook_events SET processed_at=$2, error_message=NULL WHERE id=$1`,
		id, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (GumroadWebhookEventsRepo) MarkFailed(ctx context.Context, q Querier, id uuid.UUID, errMsg string) error {
	tag, err := q.Exec(ctx,
		`UPDATE gumroad_webhook_events SET error_message=$2 WHERE id=$1`, id, errMsg)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (GumroadWebhookEventsRepo) ListUnprocessed(ctx context.Context, q Querier, limit int) ([]GumroadWebhookEvent, error) {
	rows, err := q.Query(ctx, `
		SELECT id, gumroad_event_id, event_type, payload, processed_at, error_message, received_at
		FROM gumroad_webhook_events
		WHERE processed_at IS NULL
		ORDER BY received_at
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GumroadWebhookEvent
	for rows.Next() {
		var e GumroadWebhookEvent
		if err := rows.Scan(&e.ID, &e.GumroadEventID, &e.EventType, &e.Payload,
			&e.ProcessedAt, &e.ErrorMessage, &e.ReceivedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
