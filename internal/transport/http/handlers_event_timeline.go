package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"firefreeforest/backend/internal/domain"
	"firefreeforest/backend/internal/repository"
)

const eventTimelinePreviewLimit = 5

type eventDetailResponse struct {
	domain.Event
	TimelinePreview []eventTimelineItem `json:"timeline_preview"`
	TimelineTotal   int64               `json:"timeline_total"`
	TimelineHasMore bool                `json:"timeline_has_more"`
}

type eventTimelineActor struct {
	Type string          `json:"type"`
	ID   *uuid.UUID      `json:"id,omitempty"`
	Name string          `json:"name"`
	Role domain.UserRole `json:"role,omitempty"`
}

type eventTimelineItem struct {
	ID          uint64                       `json:"id"`
	EventID     uuid.UUID                    `json:"event_id"`
	Kind        domain.EventTimelineKind     `json:"kind"`
	Title       string                       `json:"title"`
	Description string                       `json:"description"`
	Outcome     *domain.EventTimelineOutcome `json:"outcome,omitempty"`
	Level       domain.EventTimelineLevel    `json:"level"`
	Actor       eventTimelineActor           `json:"actor"`
	Metadata    json.RawMessage              `json:"metadata,omitempty"`
	OccurredAt  time.Time                    `json:"occurred_at"`
}

func newEventTimelineItem(t domain.EventTimeline) eventTimelineItem {
	return eventTimelineItem{
		ID:          t.ID,
		EventID:     t.EventID,
		Kind:        t.Kind,
		Title:       t.Title,
		Description: t.Message,
		Outcome:     t.Outcome,
		Level:       t.Level,
		Actor: eventTimelineActor{
			Type: string(t.ActorType),
			ID:   t.ActorID,
			Name: t.ActorName,
			Role: t.ActorRole,
		},
		Metadata:   t.Metadata,
		OccurredAt: t.OccurredAt,
	}
}

func newEventTimelineItems(rows []domain.EventTimeline) []eventTimelineItem {
	items := make([]eventTimelineItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, newEventTimelineItem(row))
	}
	return items
}

func (h *Handler) listEventTimeline(c *gin.Context) {
	id, ok := parseUUIDParam(c, "eventID")
	if !ok {
		return
	}
	if _, err := h.repo.GetEvent(c.Request.Context(), id); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			respondError(c, http.StatusNotFound, "not_found", "Event tidak ditemukan")
			return
		}
		respondError(c, http.StatusInternalServerError, "internal_error", "Terjadi kesalahan internal")
		return
	}

	include := strings.ToLower(strings.TrimSpace(c.Query("include")))
	if include == "" {
		include = "all"
	}
	if include != "all" && include != "important" {
		respondError(c, http.StatusUnprocessableEntity, "validation_error", "include harus all atau important")
		return
	}

	page, perPage, limit, offset := pageParams(c)
	rows, total, err := h.repo.ListEventTimeline(c.Request.Context(), id, include, limit, offset)
	if err != nil {
		h.log.Error("list event timeline failed", "event_id", id, "error", err)
		respondError(c, http.StatusInternalServerError, "internal_error", "Terjadi kesalahan internal")
		return
	}
	respondList(c, newEventTimelineItems(rows), page, perPage, total)
}
