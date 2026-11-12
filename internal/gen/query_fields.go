package gen

import (
	"fmt"
	"go/token"
	"go/types"
	"reflect"
	"strings"

	"github.com/go-kanna/internal/diag"

	"github.com/go-kanna/orm/internal/snapshot"
)

func queryColumns(fset *token.FileSet, row types.Type) ([]column, []diag.Diag) {
	var columns []column
	var ds []diag.Diag
	var walk func(*types.Struct, []string)
	walk = func(st *types.Struct, path []string) {
		for i := range st.NumFields() {
			f := st.Field(i)
			p := append(append([]string(nil), path...), f.Name())
			pos := fieldPosition(fset, f)
			tag, err := parseTag(st.Tag(i))
			switch t := f.Type(); {
			case err != nil:
				ds = append(ds, diag.Errorf(pos, "%s: %v", strings.Join(p, "."), err))
			case f.Name() == "_", tag.skip:
			case !f.Exported():
				ds = append(ds, diag.Errorf(pos, "%s: the code kanna-orm writes cannot reach an unexported field",
					strings.Join(p, ".")).WithHints(`export it, or leave it out with orm:"-"`))
			case f.Embedded() && isModel(t):
				if written := reflect.StructTag(st.Tag(i)).Get("orm"); written != "" {
					ds = append(ds, diag.Errorf(pos, `%s: orm:%q is on an embedded struct, whose fields hold the columns`,
						strings.Join(p, "."), written).WithHints("tag those fields instead"))
					continue
				}
				walk(t.Underlying().(*types.Struct), p) //nolint:forcetypeassert // isModel checks it
			case f.Embedded() && isPointerTo(t, isModel):
				ds = append(ds, diag.Errorf(pos, "%s: an embedded pointer may be nil, and so cannot be read into",
					strings.Join(p, ".")).WithHints(fmt.Sprintf(`embed %s itself, or leave it out with orm:"-"`, f.Name())))
			case holdsColumn(t):
				name := tag.column
				if name == "" {
					name = columnName(f.Name())
				}
				if tag.nullzero && !isBasic(t.Underlying(), types.String) {
					ds = append(ds, diag.Errorf(pos, "%s: nullzero requires a string field", strings.Join(p, ".")))
					continue
				}
				columns = append(columns, column{path: p, typ: t, pos: pos,
					col: snapshot.Column{Name: name}, nullzero: tag.nullzero, relationValue: tag.relation != ""})
			case holdsModels(t):
				// A relation, which the query does not read and With loads.
			default:
				ds = append(ds, diag.Errorf(pos, "%s: %s is neither a column nor a relation", strings.Join(p, "."),
					types.TypeString(t, relativeTo(f.Pkg())),
				).WithHints(`hold the column in a type with a Scan method, or leave the field out with orm:"-"`))
			}
		}
	}
	walk(row.Underlying().(*types.Struct), nil) //nolint:forcetypeassert // callers pass structs
	return columns, ds
}

type queryArgument struct {
	parameter string
	typ       types.Type
	field     *column
}

func queryArguments(fset *token.FileSet, params *types.Tuple) (map[string]queryArgument, map[string]bool, []diag.Diag) {
	args := map[string]queryArgument{}
	ambiguous := map[string]bool{}
	var ds []diag.Diag
	for i := 1; i < params.Len(); i++ {
		p := params.At(i)
		args[p.Name()] = queryArgument{parameter: p.Name(), typ: p.Type()}
	}
	for i := 1; i < params.Len(); i++ {
		p := params.At(i)
		if _, ok := p.Type().Underlying().(*types.Struct); !ok || holdsColumn(p.Type()) {
			continue
		}
		columns, cds := queryColumns(fset, p.Type())
		ds = append(ds, cds...)
		for _, c := range columns {
			if c.relationValue {
				continue
			}
			name := lowerFirst(c.path[len(c.path)-1])
			if _, ok := args[name]; ok {
				ambiguous[name] = true
			}
			args[name] = queryArgument{parameter: p.Name(), typ: c.typ, field: new(c)}
		}
	}
	return args, ambiguous, ds
}
