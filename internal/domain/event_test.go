package domain

import (
	"sync"
	"testing"

	"gorm.io/gorm/schema"
)

func TestEventDeviceRelationIsBelongsTo(t *testing.T) {
	s, err := schema.Parse(&Event{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatalf("parse event schema: %v", err)
	}
	rel, ok := s.Relationships.Relations["Device"]
	if !ok {
		t.Fatal("Device relation not found")
	}
	if rel.Type != schema.BelongsTo {
		t.Fatalf("Device relation type = %v, want belongs_to", rel.Type)
	}
	if len(rel.References) != 1 {
		t.Fatalf("Device references = %d, want 1", len(rel.References))
	}
	ref := rel.References[0]
	if ref.ForeignKey.Schema.Name != "Event" || ref.ForeignKey.Name != "DeviceID" ||
		ref.PrimaryKey.Schema.Name != "Device" || ref.PrimaryKey.Name != "ID" {
		t.Fatalf("relation = %s.%s -> %s.%s, want Event.DeviceID -> Device.ID",
			ref.ForeignKey.Schema.Name, ref.ForeignKey.Name, ref.PrimaryKey.Schema.Name, ref.PrimaryKey.Name)
	}
}
