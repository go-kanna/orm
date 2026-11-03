package impl

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/go-kanna/orm"
)

// Single selects zero or one related row. One and First differ in how extra matches are handled.
type Single[M any] struct {
	scope    Of[M]
	required bool
}
type Many[M any] struct{ scope Of[M] }

func (q Of[M]) One() Single[M] {
	if q.hasLimit || q.hasOffset ||
		q.seeking {
		q.err = errors.Join(q.err,
			errors.New("orm: One cannot hide matches with a limit, offset, or cursor; use First"))
	}
	return Single[M]{scope: q}
}
func (q Of[M]) First() Single[M] {
	if len(q.orders) == 0 {
		q.err = errors.Join(q.err, errors.New("orm: First requires an order"))
	}
	if q.hasLimit {
		q.err = errors.Join(q.err, errors.New("orm: First chooses its own limit"))
	}
	if q.seeking {
		q.err = errors.Join(q.err, errors.New("orm: First does not accept a cursor"))
	}
	return Single[M]{scope: q.Limit(1)}
}
func (q Of[M]) Many() Many[M]           { return Many[M]{scope: q} }
func (s Single[M]) Required() Single[M] { s.required = true; return s }

type projection interface {
	render(d Dialect, first int, alias string) (string, []any, error)
	isRequired() bool
}
type scalarProjection[M any] struct {
	table, column string
	parent        Parent
	keys, foreign []string
	single        Single[M]
}

func (p scalarProjection[M]) isRequired() bool { return p.single.required }
func (p scalarProjection[M]) render(d Dialect, first int, alias string) (string, []any, error) {
	q := p.single.scope
	if len(q.preloads) > 0 || len(q.projections) > 0 ||
		len(q.batches) > 0 {
		return "", nil, errors.New("orm: a scalar selection cannot preload other fields")
	}
	q.parent = &parentContext{p.parent, alias}
	for i, key := range p.keys {
		q = q.Where(quoted(d, p.foreign[i])+" = ", columnReference{p.parent, key})
	}
	rd, err := q.Render(d, first)
	if err != nil {
		return "", nil, err
	}
	// The parent alias differs from the source table even for a self-reference.
	sql := "(SELECT " + quoted(d, p.column) + " FROM " + quoted(d, p.table) + " AS " +
		quoted(d, "orm_related") + rd.Where() +
		rd.OrderBy() + rd.Limit() + ")"
	return sql, rd.Args(), nil
}

// Select attaches a correlated scalar projection. It is used by generated Select<Field> methods.
func Select[M, C any](q Of[M], field, table, column string, parent Parent, keys, foreign []string, s Single[C]) Of[M] {
	if len(keys) == 0 || len(keys) != len(foreign) ||
		parent.identity == nil {
		q.err = errors.Join(q.err, errors.New("orm: invalid scalar relation keys"))
		return q
	}
	for _, p := range q.projections {
		if p.field == field {
			q.err = errors.Join(q.err,
				fmt.Errorf("orm: %s is selected twice", field))
			return q
		}
	}
	q.projections = append(slices.Clip(q.projections), projectionField{field, scalarProjection[C]{table, column,
		parent, slices.Clone(keys), slices.Clone(foreign), s}})
	return q
}

type projectionField struct {
	field string
	value projection
}

// Projection describes one generated result field. Optional says its Go type can hold NULL.
type Projection struct {
	Field, Column string
	Optional      bool
}

// RenderSelect renders a model read, including correlated scalar values before the root placeholders.
func (q Of[M]) RenderSelect(d Dialect, table string, columns []string, fields []Projection) (string, []any, error) {
	names := make([]string, 0, len(columns)+len(fields))
	for _, name := range columns {
		names = append(names, qualified(d, "orm_parent", name))
	}
	projected, args, err := q.renderProjections(d, fields, "orm_parent", 1)
	if err != nil {
		return "", nil, err
	}
	names = append(names, projected...)
	rd, err := q.Render(d, len(args)+1)
	if err != nil {
		return "", nil, err
	}
	text := "SELECT " + strings.Join(names, ", ") + " FROM " + quoted(d, table) + " AS " + quoted(d, "orm_parent")
	return text + rd.Where() + rd.OrderBy() + rd.Limit(), append(args, rd.Args()...), nil
}

func (q Of[M]) renderProjections(d Dialect, fields []Projection, alias string, first int) ([]string, []any, error) {
	seen := map[string]bool{}
	for _, p := range q.projections {
		if seen[p.field] {
			return nil, nil, fmt.Errorf("orm: %s is selected twice", p.field)
		}
		seen[p.field] = true
		if !slices.ContainsFunc(fields, func(f Projection) bool { return f.Field == p.field }) {
			return nil, nil, fmt.Errorf("orm: unknown selected field %s", p.field)
		}
	}
	names := make([]string, 0, len(fields))
	args := []any{}
	for _, f := range fields {
		text := "NULL"
		for _, p := range q.projections {
			if p.field != f.Field {
				continue
			}
			if !f.Optional &&
				!p.value.isRequired() {
				return nil, nil, fmt.Errorf("orm: %s requires Required or a nullable field", f.Field)
			}
			sql, values, err := p.value.render(d, first+len(args), alias)
			if err != nil {
				return nil, nil, fmt.Errorf("select %s: %w", f.Field, err)
			}
			text = sql
			args = append(args, values...)
		}
		names = append(names, text+" AS "+quoted(d, f.Column))
	}
	return names, args, nil
}

func CheckSelection[M any](q Of[M], field string, missing bool) error {
	for _, p := range q.projections {
		if p.field == field && p.value.isRequired() &&
			missing {
			return fmt.Errorf("%s: %w", field, orm.ErrRequiredRelation)
		}
	}
	return nil
}
