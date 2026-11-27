package mcp

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"firefreeforest/backend/internal/domain"
)

func TestPageParams(t *testing.T) {
	cases := []struct {
		name                string
		page, perPage       int
		wantPage, wantLimit int
		wantOffset          int
	}{
		{"defaults", 0, 0, 1, defaultPerPage, 0},
		{"negative page", -3, 10, 1, 10, 0},
		{"cap per page", 2, 500, 2, maxPerPage, maxPerPage},
		{"second page", 3, 25, 3, 25, 50},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			page, perPage, limit, offset := pageParams(tc.page, tc.perPage)
			if page != tc.wantPage || limit != tc.wantLimit || offset != tc.wantOffset {
				t.Fatalf("pageParams(%d,%d) = page %d, perPage %d, limit %d, offset %d",
					tc.page, tc.perPage, page, perPage, limit, offset)
			}
			if perPage != limit {
				t.Fatalf("perPage = %d, limit = %d; want equal", perPage, limit)
			}
		})
	}
}

func TestParseOptionalTime(t *testing.T) {
	if got, err := parseOptionalTime("   ", "from"); err != nil || got != nil {
		t.Fatalf("blank = %v, %v", got, err)
	}
	got, err := parseOptionalTime("2026-11-21T10:00:00+07:00", "from")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.Location() != time.UTC {
		t.Fatalf("location = %v, want UTC", got.Location())
	}
	if want := time.Date(2026, 11, 21, 3, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("time = %v, want %v", got, want)
	}
	if _, err := parseOptionalTime("21-11-2026", "to"); err == nil {
		t.Fatal("expected error for invalid timestamp")
	}
}

func TestParseUUIDField(t *testing.T) {
	id := uuid.New()
	got, err := parseUUIDField("  "+id.String()+"  ", "event_id")
	if err != nil || got != id {
		t.Fatalf("parse = %v, %v; want %v", got, err, id)
	}
	if _, err := parseUUIDField("not-a-uuid", "event_id"); err == nil {
		t.Fatal("expected error for invalid UUID")
	}
}

func TestParseAggregate(t *testing.T) {
	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	from := base.Format(time.RFC3339)
	to := base.Add(2 * time.Hour).Format(time.RFC3339)

	t.Run("requires range", func(t *testing.T) {
		if _, _, _, err := parseAggregate("", to, nil); err == nil {
			t.Fatal("expected error for missing from")
		}
		if _, _, _, err := parseAggregate(from, "", nil); err == nil {
			t.Fatal("expected error for missing to")
		}
	})

	t.Run("requires ordered range", func(t *testing.T) {
		if _, _, _, err := parseAggregate(to, from, nil); err == nil {
			t.Fatal("expected error when from is after to")
		}
	})

	t.Run("default interval", func(t *testing.T) {
		gotFrom, gotTo, interval, err := parseAggregate(from, to, nil)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if interval != defaultIntervalMin {
			t.Fatalf("interval = %d, want %d", interval, defaultIntervalMin)
		}
		if !gotFrom.Equal(base) || !gotTo.Equal(base.Add(2*time.Hour)) {
			t.Fatalf("range = %v - %v", gotFrom, gotTo)
		}
	})

	t.Run("raw range", func(t *testing.T) {
		zero := 0
		shortTo := base.Add(maxRawRange).Format(time.RFC3339)
		if _, _, interval, err := parseAggregate(from, shortTo, &zero); err != nil || interval != 0 {
			t.Fatalf("raw at limit = %d, %v", interval, err)
		}
		longTo := base.Add(maxRawRange + time.Minute).Format(time.RFC3339)
		if _, _, _, err := parseAggregate(from, longTo, &zero); err == nil {
			t.Fatal("expected error for raw range over 7 days")
		}
	})

	t.Run("interval bounds", func(t *testing.T) {
		negative := -1
		if _, _, _, err := parseAggregate(from, to, &negative); err == nil {
			t.Fatal("expected error for negative interval")
		}
		tooLarge := maxIntervalMin + 1
		if _, _, _, err := parseAggregate(from, to, &tooLarge); err == nil {
			t.Fatal("expected error for interval above maximum")
		}
	})

	t.Run("maximum span", func(t *testing.T) {
		longTo := base.Add(maxAggregateRange + time.Hour).Format(time.RFC3339)
		interval := 60
		if _, _, _, err := parseAggregate(from, longTo, &interval); err == nil {
			t.Fatal("expected error for span over 90 days")
		}
	})

	t.Run("maximum buckets", func(t *testing.T) {
		ninetyDays := base.Add(90 * 24 * time.Hour).Format(time.RFC3339)
		interval := 1
		if _, _, _, err := parseAggregate(from, ninetyDays, &interval); err == nil {
			t.Fatal("expected error for too many buckets")
		}
	})
}

func TestJSONResult(t *testing.T) {
	result, out, err := jsonResult(map[string]any{"ok": true})
	if err != nil {
		t.Fatalf("jsonResult: %v", err)
	}
	if out != nil {
		t.Fatalf("out = %v, want nil", out)
	}
	if result == nil || len(result.Content) != 1 {
		t.Fatalf("result = %+v", result)
	}
	text, ok := result.Content[0].(*sdkmcp.TextContent)
	if !ok {
		t.Fatalf("content type = %T", result.Content[0])
	}
	var decoded map[string]bool
	if err := json.Unmarshal([]byte(text.Text), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !decoded["ok"] {
		t.Fatalf("decoded = %+v", decoded)
	}
}

func TestNewTimelineItems(t *testing.T) {
	actorID := uuid.New()
	eventID := uuid.New()
	outcome := domain.TimelineOutcomeSent
	occurred := time.Date(2026, 11, 21, 9, 30, 0, 0, time.UTC)
	rows := []domain.EventTimeline{{
		ID:         7,
		EventID:    eventID,
		Kind:       domain.TimelineNotification,
		Outcome:    &outcome,
		ActorType:  domain.TimelineActorUser,
		ActorID:    &actorID,
		ActorName:  "Petugas Satu",
		ActorRole:  domain.RolePetugas,
		Title:      "Notifikasi terkirim",
		Message:    "FCM terkirim ke 2 perangkat",
		Level:      domain.TimelineLevelSuccess,
		Metadata:   json.RawMessage(`{"sent":2}`),
		OccurredAt: occurred,
	}}

	items := newTimelineItems(rows)
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	item := items[0]
	if item.ID != 7 || item.EventID != eventID || item.Kind != domain.TimelineNotification {
		t.Fatalf("item = %+v", item)
	}
	if item.Description != rows[0].Message {
		t.Fatalf("description = %q", item.Description)
	}
	if item.Actor.ID == nil || *item.Actor.ID != actorID || item.Actor.Role != domain.RolePetugas {
		t.Fatalf("actor = %+v", item.Actor)
	}
	if item.OccurredAt != occurred {
		t.Fatalf("occurred_at = %v", item.OccurredAt)
	}

	if items := newTimelineItems(nil); items == nil || len(items) != 0 {
		t.Fatalf("nil rows = %#v, want empty slice", items)
	}
}

func TestDeviceIDFromLatestURI(t *testing.T) {
	cases := []struct {
		uri     string
		want    string
		wantErr bool
	}{
		{"firefreeforest://devices/FFF-001/latest", "FFF-001", false},
		{"firefreeforest://devices/FFF-001/latest/", "FFF-001", false},
		{"firefreeforest://devices/FFF-001/status", "", true},
		{"firefreeforest://devices/latest", "", true},
		{"firefreeforest://devices//latest", "", true},
		{"firefreeforest://devices", "", true},
	}
	for _, tc := range cases {
		got, err := deviceIDFromLatestURI(tc.uri)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("%q: expected error, got %q", tc.uri, got)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Fatalf("%q = %q, %v; want %q", tc.uri, got, err, tc.want)
		}
	}
}
