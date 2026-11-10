package gen

import (
	"fmt"
	"go/types"
	"slices"
	"strings"

	"github.com/go-kanna/internal/diag"
)

// audit tells whether a column is the time its row was created or the time it was last updated, which kanna-orm
// writes by the clock of the repository.
type audit int

const (
	auditNone audit = iota
	auditCreated
	auditUpdated
)

// field is the name of the fields of a column of a, which hold it when no field of their model is tagged a.
func (a audit) field() string {
	if a == auditCreated {
		return "CreatedAt"
	}
	return "UpdatedAt"
}

// tag is the option of the orm tag marking a column of a.
func (a audit) tag() string {
	if a == auditCreated {
		return "created"
	}
	return "updated"
}

// stamped reports whether kanna-orm writes c by the clock: c is an audit column, which the database does not assign.
func (c column) stamped() bool {
	return c.audit != auditNone && !c.insertDefault && !c.col.Assigned
}

// stamps reports whether kanna-orm writes any of cols by the clock.
func stamps(cols []column) bool {
	return slices.ContainsFunc(cols, column.stamped)
}

// clocked reports whether m writes a column by the clock, which it reads once, before it writes.
func (m repoMethod) clocked() bool {
	switch m.verb { //nolint:exhaustive // the others write no audit column by the clock
	case verbInsert, verbUpsert:
		return stamps(m.inserted())
	case verbUpdate:
		return stamps(m.updated())
	}
	return false
}

// touched is the update time a write of a change set of m writes by the clock when the change set writes any column,
// if m has one.
func (m repoMethod) touched() (column, bool) {
	for _, c := range m.updated() {
		if c.stamped() {
			return c, true
		}
	}
	return column{}, false
}

// readClock writes the read of the clock into sig.now, if the method writes a column by it.
func (e repoEmitter) readClock(recv string, sig signature) {
	if sig.now != "" {
		e.printf("%s := %s.db.Now(%s)\n", sig.now, recv, sig.ctx)
	}
}

// written is what a write gives c of the row row: the clock's time, now, of an audit column kanna-orm writes, or the
// row's value.
func (e repoEmitter) written(c column, row, now string) string {
	if c.stamped() {
		return now
	}
	return e.storageValue(c, row+"."+fieldPath(c))
}

// changesOf is the change set, of the parameter changes, an update or an upsert of m writes: the one given, as the
// runtime reads it, writing the update time too, now, when it writes any column.
func (e repoEmitter) changesOf(m repoMethod, changes, now string) string {
	of := fmt.Sprintf("%s[%s](%s.Of())", e.ref("Changes"), types.TypeString(m.model, e.f.Qualifier), changes)
	if c, ok := m.touched(); ok {
		of += fmt.Sprintf(".Touch(%q, %s)", c.col.Name, now)
	}
	return of
}

// audits settles the audit columns of the model: of each kind, the column tagged so, or, when none is, the column of
// the field named so. It reports two of a kind, one that is no time.Time, and one tagged insertonly.
func (m *matcher) audits() {
	for _, kind := range []audit{auditCreated, auditUpdated} {
		tagged := slices.ContainsFunc(m.columns, func(c column) bool { return c.audit == kind })
		held := ""
		for i, c := range m.columns {
			named := !tagged && c.audit == auditNone && c.path[len(c.path)-1] == kind.field()
			if c.audit != kind && !named {
				continue
			}
			m.columns[i].audit = kind
			label := m.sc.model.Obj().Name() + "." + strings.Join(c.path, ".")
			if !isTime(c.typ) {
				typ := types.TypeString(c.typ, relativeTo(m.sc.model.Obj().Pkg()))
				d := diag.Errorf(c.pos, "%s: %s, the time a row is %s, is a time.Time, not %s %s", label, c.col.Name,
					kind.tag(), article(typ), typ).WithHints("declare it as time.Time")
				if named {
					d = d.WithHints(fmt.Sprintf(`or, for a column kanna-orm leaves to you, name the field otherwise, `+
						`holding the column by orm:"%s"`, c.col.Name))
				}
				m.ds = append(m.ds, d)
			}
			if c.insertOnly {
				what := "an insert alone writes it already"
				if kind == auditUpdated {
					what = "every write of its row writes it, which insertonly would leave out"
				}
				m.ds = append(m.ds, diag.Errorf(c.pos, "%s: %s is the time a row is %s, and %s", label, c.col.Name,
					kind.tag(), what).WithHints("remove insertonly"))
			}
			if held == "" {
				held = label
				continue
			}
			hint := "tag one field alone " + kind.tag()
			if named {
				hint = `leave one of them out with orm:"-", or tag the one meant ` + kind.tag()
			}
			m.ds = append(m.ds, diag.Errorf(c.pos, "%s: %s is the time a row of %s is %s already", label, held,
				m.sc.table, kind.tag()).WithHints(hint))
		}
	}
}
