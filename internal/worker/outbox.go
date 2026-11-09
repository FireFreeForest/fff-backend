package worker

import (
	"context"
	"time"

	"github.com/google/uuid"

	"firefreeforest/backend/internal/observability"
)

const maxOutboxAttempts = 10

func (w *Worker) runOutbox(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	w.dispatchOutbox(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.dispatchOutbox(ctx)
		}
	}
}

func (w *Worker) dispatchOutbox(ctx context.Context) {
	rows, err := w.repo.ClaimOutbox(ctx, 20)
	if err != nil {
		w.log.Error("failed to claim outbox", "error", err)
		return
	}
	for _, row := range rows {
		if err := w.mqtt.Publish(ctx, row.Topic, byte(row.QoS), row.Payload); err != nil {
			observability.OutboxPublished.WithLabelValues("error").Inc()
			if merr := w.repo.MarkOutboxFailed(ctx, row, err.Error(), maxOutboxAttempts); merr != nil {
				w.log.Error("failed to mark outbox failure", "id", row.ID, "error", merr)
			}
			w.log.Warn("outbox publish failed", "id", row.ID, "topic", row.Topic, "attempts", row.Attempts+1, "error", err)
			continue
		}

		observability.OutboxPublished.WithLabelValues("sent").Inc()
		if err := w.repo.MarkOutboxSent(ctx, row.ID); err != nil {
			w.log.Error("failed to mark outbox sent", "id", row.ID, "error", err)
		}
		if row.AggregateType == "command" {
			if id, err := uuid.Parse(row.AggregateID); err == nil {
				if err := w.repo.MarkCommandSent(ctx, id); err != nil {
					w.log.Warn("failed to mark command sent", "command_uuid", id, "error", err)
				}
			}
		}
	}
}
