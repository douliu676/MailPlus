package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// SystemSetting holds the schema definition for the SystemSetting entity.
type SystemSetting struct {
	ent.Schema
}

// Fields of the SystemSetting.
func (SystemSetting) Fields() []ent.Field {
	return []ent.Field{
		field.String("key").NotEmpty().Unique(),
		field.String("value").Default(""),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

// Edges of the SystemSetting.
func (SystemSetting) Edges() []ent.Edge {
	return nil
}

// Indexes of the SystemSetting.
func (SystemSetting) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("key").Unique(),
	}
}
