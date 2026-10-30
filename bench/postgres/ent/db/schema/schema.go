// Package schema declares the tables of the benchmarks to ent, which generates the client in .. from it. The tables
// themselves are those of ../../../migrate.
package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

type Customer struct{ ent.Schema }

func (Customer) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id"),
		field.String("email").Unique(),
		field.String("name"),
		field.Time("created_at"),
	}
}

type Order struct{ ent.Schema }

func (Order) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id"),
		field.Int64("customer_id"),
		field.String("status"),
		field.Time("placed_at"),
		field.String("note").Optional().Nillable(),
	}
}

func (Order) Edges() []ent.Edge {
	return []ent.Edge{edge.To("lines", OrderLine.Type)}
}

type OrderLine struct{ ent.Schema }

func (OrderLine) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id"),
		field.Int64("order_id"),
		field.String("sku"),
		field.Int32("quantity"),
	}
}

func (OrderLine) Edges() []ent.Edge {
	return []ent.Edge{edge.From("order", Order.Type).Ref("lines").Field("order_id").Unique().Required()}
}
